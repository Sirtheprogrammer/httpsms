package handler

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/httpsms/gateway-server/internal/api/ws"
	"github.com/httpsms/gateway-server/internal/domain"
	"github.com/httpsms/gateway-server/internal/storage"
	"github.com/httpsms/gateway-server/internal/webhook"
)

type APIHandler struct {
	store      *storage.DB
	hub        *ws.Hub
	dispatcher *webhook.Dispatcher
	otaDir     string
}

func NewAPIHandler(store *storage.DB, hub *ws.Hub, dispatcher *webhook.Dispatcher, otaDir string) *APIHandler {
	_ = os.MkdirAll(otaDir, 0755)
	return &APIHandler{
		store:      store,
		hub:        hub,
		dispatcher: dispatcher,
		otaDir:     otaDir,
	}
}

// WriteJSON sends a JSON response with the given status code
func WriteJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, map[string]interface{}{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

// SendSMS handles POST /api/v1/sms/send
func (h *APIHandler) SendSMS(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Recipient      string `json:"recipient"`
		Body           string `json:"body"`
		DeviceID       string `json:"device_id"`
		SimSlot        int    `json:"sim_slot"`
		IdempotencyKey string `json:"idempotency_key"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "Malformed JSON body")
		return
	}

	if req.Recipient == "" || req.Body == "" {
		WriteError(w, http.StatusBadRequest, "VALIDATION_FAILED", "recipient and body are required")
		return
	}

	if req.IdempotencyKey == "" {
		req.IdempotencyKey = uuid.New().String()
	}

	// Calculate segments: standard GSM 7-bit is 160 chars, UCS-2 (Unicode) is 70 chars
	isUnicode := false
	for _, c := range req.Body {
		if c > 127 {
			isUnicode = true
			break
		}
	}
	partsCount := 1
	if isUnicode {
		if len([]rune(req.Body)) > 70 {
			partsCount = (len([]rune(req.Body)) + 66) / 67 // 67 chars per part for multipart unicode
		}
	} else {
		if len(req.Body) > 160 {
			partsCount = (len(req.Body) + 152) / 153 // 153 chars per part for multipart GSM
		}
	}

	msgID := uuid.New().String()
	msg := domain.Message{
		ID:             msgID,
		Direction:      domain.DirectionOutbound,
		DeviceID:       req.DeviceID,
		SimSlot:        req.SimSlot,
		Recipient:      req.Recipient,
		Body:           req.Body,
		Status:         domain.MessageStatusQueued,
		IdempotencyKey: req.IdempotencyKey,
		PartsCount:     partsCount,
		CreatedAt:      time.Now().UTC(),
	}

	isNew, err := h.store.SaveMessage(&msg)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to save message")
		return
	}

	if !isNew {
		// Existing idempotency key found
		existing, _ := h.store.GetMessage(msg.ID)
		WriteJSON(w, http.StatusOK, existing)
		return
	}

	// Dispatch to connected Android device
	if err := h.hub.SendSmsCommand(&msg); err != nil {
		if err == ws.ErrDeviceOffline {
			_ = h.store.UpdateMessageStatus(msg.ID, domain.MessageStatusFailed, "Android gateway device is offline", nil, nil)
			WriteError(w, http.StatusServiceUnavailable, "DEVICE_OFFLINE", "No online Android gateway device available to send SMS")
			return
		}
		_ = h.store.UpdateMessageStatus(msg.ID, domain.MessageStatusFailed, err.Error(), nil, nil)
		WriteError(w, http.StatusInternalServerError, "DISPATCH_FAILED", err.Error())
		return
	}

	WriteJSON(w, http.StatusAccepted, msg)
}

// ListMessages handles GET /api/v1/messages
func (h *APIHandler) ListMessages(w http.ResponseWriter, r *http.Request) {
	direction := r.URL.Query().Get("direction")
	search := r.URL.Query().Get("search")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	if limit <= 0 || limit > 100 {
		limit = 30
	}
	if offset < 0 {
		offset = 0
	}

	messages, total, err := h.store.ListMessages(direction, search, limit, offset)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"data":   messages,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// GetMessage handles GET /api/v1/messages/{id}
func (h *APIHandler) GetMessage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	msg, err := h.store.GetMessage(id)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	if msg == nil {
		WriteError(w, http.StatusNotFound, "NOT_FOUND", "Message not found")
		return
	}
	WriteJSON(w, http.StatusOK, msg)
}

// ListDevices handles GET /api/v1/devices
func (h *APIHandler) ListDevices(w http.ResponseWriter, r *http.Request) {
	devices, err := h.store.ListDevices()
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, devices)
}

// PairDevice handles POST /api/v1/devices/pair
// Generates a new device registration record with a dedicated token for QR code scanning
func (h *APIHandler) PairDevice(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Name == "" {
		req.Name = "Android Gateway Phone"
	}

	// Generate 32-byte secure random token
	tokenBytes := make([]byte, 24)
	_, _ = rand.Read(tokenBytes)
	rawToken := "htdev_" + hex.EncodeToString(tokenBytes)

	tokenHashBytes := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(tokenHashBytes[:])

	deviceID := uuid.New().String()
	device := domain.Device{
		ID:        deviceID,
		Name:      req.Name,
		TokenHash: tokenHash,
		Status:    "offline",
		CreatedAt: time.Now().UTC(),
	}

	if err := h.store.UpsertDevice(&device); err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to register device")
		return
	}

	WriteJSON(w, http.StatusCreated, map[string]interface{}{
		"device_id": deviceID,
		"name":      device.Name,
		"token":     rawToken, // Plaintext shown only once for pairing QR code
	})
}

// DeleteDevice handles DELETE /api/v1/devices/{id}
func (h *APIHandler) DeleteDevice(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.store.DeleteDevice(id); err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// Webhook Handlers
func (h *APIHandler) ListWebhooks(w http.ResponseWriter, r *http.Request) {
	whs, err := h.store.ListWebhooks()
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, whs)
}

func (h *APIHandler) CreateWebhook(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL    string   `json:"url"`
		Secret string   `json:"secret"`
		Events []string `json:"events"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URL == "" {
		WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "URL is required")
		return
	}

	if len(req.Events) == 0 {
		req.Events = []string{"*"}
	}

	if req.Secret == "" {
		secBytes := make([]byte, 16)
		_, _ = rand.Read(secBytes)
		req.Secret = "whsec_" + hex.EncodeToString(secBytes)
	}

	wh := domain.Webhook{
		ID:        uuid.New().String(),
		URL:       req.URL,
		Secret:    req.Secret,
		Events:    req.Events,
		IsActive:  true,
		CreatedAt: time.Now().UTC(),
	}

	if err := h.store.CreateWebhook(&wh); err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	WriteJSON(w, http.StatusCreated, wh)
}

func (h *APIHandler) DeleteWebhook(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.store.DeleteWebhook(id); err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (h *APIHandler) TestWebhook(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	whs, err := h.store.ListWebhooks()
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	var target *domain.Webhook
	for _, wh := range whs {
		if wh.ID == id {
			target = &wh
			break
		}
	}

	if target == nil {
		WriteError(w, http.StatusNotFound, "NOT_FOUND", "Webhook not found")
		return
	}

	delivery, err := h.dispatcher.TestWebhook(target)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "DELIVERY_ERROR", err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, delivery)
}

func (h *APIHandler) ListWebhookDeliveries(w http.ResponseWriter, r *http.Request) {
	webhookID := r.URL.Query().Get("webhook_id")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	deliveries, err := h.store.ListWebhookDeliveries(webhookID, limit)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, deliveries)
}

// API Token Handlers
func (h *APIHandler) ListTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := h.store.ListApiTokens()
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, tokens)
}

func (h *APIHandler) CreateToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Name == "" {
		req.Name = "Default API Token"
	}
	if len(req.Scopes) == 0 {
		req.Scopes = []string{domain.ScopeSMSSend, domain.ScopeSMSRead}
	}

	rawSecretBytes := make([]byte, 24)
	_, _ = rand.Read(rawSecretBytes)
	rawToken := "htsms_live_" + hex.EncodeToString(rawSecretBytes)

	hBytes := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(hBytes[:])

	token := domain.ApiToken{
		ID:          uuid.New().String(),
		Name:        req.Name,
		TokenPrefix: rawToken[:16] + "...",
		TokenHash:   tokenHash,
		Scopes:      req.Scopes,
		CreatedAt:   time.Now().UTC(),
	}

	if err := h.store.CreateApiToken(&token); err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	WriteJSON(w, http.StatusCreated, map[string]interface{}{
		"token": token,
		"key":   rawToken, // Displayed once
	})
}

func (h *APIHandler) DeleteToken(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.store.DeleteApiToken(id); err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// OTA Update Handlers
func (h *APIHandler) CheckOtaUpdate(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Query().Get("target")
	if target == "" {
		target = "android"
	}
	currentCode, _ := strconv.Atoi(r.URL.Query().Get("version_code"))

	release, err := h.store.GetLatestOtaRelease(target)
	if err != nil || release == nil {
		WriteJSON(w, http.StatusOK, map[string]interface{}{
			"update_available": false,
		})
		return
	}

	updateAvailable := release.VersionCode > currentCode
	downloadURL := fmt.Sprintf("/api/v1/ota/download/%s", release.ID)

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"update_available": updateAvailable,
		"release":          release,
		"download_url":     downloadURL,
	})
}

func (h *APIHandler) UploadOtaRelease(w http.ResponseWriter, r *http.Request) {
	// Parse multipart form (max 100MB)
	if err := r.ParseMultipartForm(100 << 20); err != nil {
		WriteError(w, http.StatusBadRequest, "FILE_TOO_LARGE", "Max upload size is 100MB")
		return
	}

	target := r.FormValue("target")
	if target == "" {
		target = "android"
	}
	version := r.FormValue("version")
	versionCode, _ := strconv.Atoi(r.FormValue("version_code"))
	changelog := r.FormValue("changelog")

	file, header, err := r.FormFile("file")
	if err != nil {
		WriteError(w, http.StatusBadRequest, "FILE_REQUIRED", "No file uploaded")
		return
	}
	defer file.Close()

	releaseID := uuid.New().String()
	destName := fmt.Sprintf("%s_%s_%s", target, releaseID, filepath.Base(header.Filename))
	destPath := filepath.Join(h.otaDir, destName)

	destFile, err := os.Create(destPath)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "STORAGE_ERROR", err.Error())
		return
	}
	defer destFile.Close()

	hasher := sha256.New()
	multiWriter := io.MultiWriter(destFile, hasher)

	fileSize, err := io.Copy(multiWriter, file)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "WRITE_ERROR", err.Error())
		return
	}

	checksum := hex.EncodeToString(hasher.Sum(nil))

	release := domain.OtaRelease{
		ID:             releaseID,
		Target:         target,
		Version:        version,
		VersionCode:    versionCode,
		FileName:       header.Filename,
		FilePath:       destPath,
		ChecksumSHA256: checksum,
		FileSize:       fileSize,
		Changelog:      changelog,
		IsActive:       true,
		CreatedAt:      time.Now().UTC(),
	}

	if err := h.store.SaveOtaRelease(&release); err != nil {
		WriteError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	// Broadcast OTA release to all connected devices and dashboards
	h.hub.BroadcastToDashboards("ota.new_release", release)

	WriteJSON(w, http.StatusCreated, release)
}

func (h *APIHandler) DownloadOtaRelease(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	releases, err := h.store.ListOtaReleases()
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var target *domain.OtaRelease
	for _, rel := range releases {
		if rel.ID == id {
			target = &rel
			break
		}
	}

	if target == nil || target.FilePath == "" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", target.FileName))
	w.Header().Set("X-Checksum-SHA256", target.ChecksumSHA256)
	http.ServeFile(w, r, target.FilePath)
}

// GetSystemStats handles GET /api/v1/system/stats
func (h *APIHandler) GetSystemStats(w http.ResponseWriter, r *http.Request) {
	devices, _ := h.store.ListDevices()
	onlineDevices := 0
	for _, d := range devices {
		if d.Status == "online" {
			onlineDevices++
		}
	}

	_, totalInbound, _ := h.store.ListMessages("inbound", "", 1, 0)
	_, totalOutbound, _ := h.store.ListMessages("outbound", "", 1, 0)
	webhooks, _ := h.store.ListWebhooks()

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"online_devices":  onlineDevices,
		"total_devices":   len(devices),
		"total_inbound":   totalInbound,
		"total_outbound":  totalOutbound,
		"active_webhooks": len(webhooks),
		"timestamp":       time.Now().UTC(),
	})
}

