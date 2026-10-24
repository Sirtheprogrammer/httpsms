package handler

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/httpsms/gateway-server/internal/api/middleware"
	"github.com/httpsms/gateway-server/internal/api/ws"
	"github.com/httpsms/gateway-server/internal/domain"
	"github.com/httpsms/gateway-server/internal/secrets"
	"github.com/httpsms/gateway-server/internal/storage"
	"github.com/httpsms/gateway-server/internal/webhook"
	"github.com/httpsms/gateway-server/internal/workflow"
)

type APIHandler struct {
	store      *storage.DB
	hub        *ws.Hub
	dispatcher *webhook.Dispatcher
	engine     *workflow.Engine
	box        *secrets.Box
	otaDir     string
}

func NewAPIHandler(store *storage.DB, hub *ws.Hub, dispatcher *webhook.Dispatcher, engine *workflow.Engine, box *secrets.Box, otaDir string) *APIHandler {
	_ = os.MkdirAll(otaDir, 0755)
	return &APIHandler{
		store:      store,
		hub:        hub,
		dispatcher: dispatcher,
		engine:     engine,
		box:        box,
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

func principal(r *http.Request) *middleware.Principal {
	return middleware.FromContext(r.Context())
}

// decodeJSON reads a size-limited JSON body into v.
func decodeJSON(w http.ResponseWriter, r *http.Request, v interface{}) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "Malformed JSON body")
		return false
	}
	return true
}

func notFound(w http.ResponseWriter, what string) {
	WriteError(w, http.StatusNotFound, "NOT_FOUND", what+" not found")
}

// smsParts estimates segments: GSM-7 is 160 chars (153 per part), UCS-2 is 70 (67 per part).
func smsParts(body string) int {
	runes := []rune(body)
	for _, c := range runes {
		if c > 127 {
			if len(runes) > 70 {
				return (len(runes) + 66) / 67
			}
			return 1
		}
	}
	if len(body) > 160 {
		return (len(body) + 152) / 153
	}
	return 1
}

var ErrNoDevice = errors.New("no online phone is available to send this SMS")

// queueOutbound stores an outbound SMS and hands it to the owner's phone.
func (h *APIHandler) queueOutbound(owner, deviceID string, simSlot int, to, body, idempotencyKey string) (*domain.Message, bool, error) {
	if idempotencyKey == "" {
		idempotencyKey = uuid.New().String()
	}
	if deviceID != "" {
		dev, err := h.store.GetDevice(deviceID)
		if err != nil || dev == nil || dev.UserID != owner {
			return nil, false, fmt.Errorf("phone %s not found", deviceID)
		}
	}
	msg := domain.Message{
		ID:             uuid.New().String(),
		UserID:         owner,
		Direction:      domain.DirectionOutbound,
		DeviceID:       deviceID,
		SimSlot:        simSlot,
		Recipient:      to,
		Body:           body,
		Status:         domain.MessageStatusQueued,
		IdempotencyKey: idempotencyKey,
		PartsCount:     smsParts(body),
		CreatedAt:      time.Now().UTC(),
	}

	isNew, err := h.store.SaveMessage(&msg)
	if err != nil {
		return nil, false, fmt.Errorf("failed to save message: %w", err)
	}
	if !isNew {
		existing, _ := h.store.GetMessage(msg.ID)
		return existing, false, nil
	}

	if err := h.hub.SendSmsCommand(&msg); err != nil {
		reason := err.Error()
		if err == ws.ErrDeviceOffline {
			reason = ErrNoDevice.Error()
		}
		_ = h.store.UpdateMessageStatus(msg.ID, domain.MessageStatusFailed, reason, nil, nil)
		msg.Status = domain.MessageStatusFailed
		msg.ErrorMessage = reason
		if err == ws.ErrDeviceOffline {
			return &msg, true, ErrNoDevice
		}
		return &msg, true, err
	}
	return &msg, true, nil
}

// SendFromWorkflow implements workflow.SMSSender.
func (h *APIHandler) SendFromWorkflow(owner, deviceID string, simSlot int, to, body string) (string, error) {
	// Prefer the phone that received the trigger, but fall back to any online phone.
	if deviceID != "" && !h.hub.IsOnline(deviceID) {
		deviceID = ""
	}
	msg, _, err := h.queueOutbound(owner, deviceID, simSlot, to, body, "")
	if err != nil {
		return "", err
	}
	return msg.ID, nil
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
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Recipient = strings.TrimSpace(req.Recipient)
	if req.Recipient == "" || strings.TrimSpace(req.Body) == "" {
		WriteError(w, http.StatusBadRequest, "VALIDATION_FAILED", "recipient and body are required")
		return
	}

	msg, isNew, err := h.queueOutbound(principal(r).UserID(), req.DeviceID, req.SimSlot, req.Recipient, req.Body, req.IdempotencyKey)
	switch {
	case err == ErrNoDevice:
		WriteError(w, http.StatusServiceUnavailable, "DEVICE_OFFLINE", err.Error())
	case err != nil && msg == nil:
		WriteError(w, http.StatusBadRequest, "SEND_FAILED", err.Error())
	case err != nil:
		WriteError(w, http.StatusInternalServerError, "DISPATCH_FAILED", err.Error())
	case !isNew:
		WriteJSON(w, http.StatusOK, msg)
	default:
		WriteJSON(w, http.StatusAccepted, msg)
	}
}

// ListMessages handles GET /api/v1/messages
func (h *APIHandler) ListMessages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	messages, total, err := h.store.ListMessages(storage.MessageFilter{
		Owner:     principal(r).Owner(),
		Direction: q.Get("direction"),
		Search:    strings.TrimSpace(q.Get("search")),
		Contact:   strings.TrimSpace(q.Get("contact")),
		Limit:     limit,
		Offset:    offset,
	})
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
	msg, err := h.store.GetMessage(chi.URLParam(r, "id"))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	if msg == nil || !principal(r).CanAccess(msg.UserID) {
		notFound(w, "Message")
		return
	}
	WriteJSON(w, http.StatusOK, msg)
}

// ListDevices handles GET /api/v1/devices
func (h *APIHandler) ListDevices(w http.ResponseWriter, r *http.Request) {
	devices, err := h.store.ListDevices(principal(r).Owner())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, devices)
}

// PairDevice handles POST /api/v1/devices/pair: creates a device with a one-time
// token that the phone receives by scanning the pairing QR code.
func (h *APIHandler) PairDevice(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req)
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		req.Name = "Android phone"
	}

	rawToken := middleware.NewRandomToken("htdev_", 24)
	device := domain.Device{
		ID:        uuid.New().String(),
		UserID:    principal(r).UserID(),
		Name:      req.Name,
		TokenHash: middleware.HashToken(rawToken),
		Status:    "offline",
		CreatedAt: time.Now().UTC(),
	}
	if err := h.store.UpsertDevice(&device); err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to register device")
		return
	}

	WriteJSON(w, http.StatusCreated, map[string]interface{}{
		"device_id": device.ID,
		"name":      device.Name,
		"token":     rawToken, // Plaintext shown only once for pairing QR code
	})
}

func (h *APIHandler) ownedDevice(w http.ResponseWriter, r *http.Request) *domain.Device {
	dev, err := h.store.GetDevice(chi.URLParam(r, "id"))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return nil
	}
	if dev == nil || !principal(r).CanAccess(dev.UserID) {
		notFound(w, "Device")
		return nil
	}
	return dev
}

// UpdateDevice handles PATCH /api/v1/devices/{id} (rename)
func (h *APIHandler) UpdateDevice(w http.ResponseWriter, r *http.Request) {
	dev := h.ownedDevice(w, r)
	if dev == nil {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 80 {
		WriteError(w, http.StatusBadRequest, "VALIDATION_FAILED", "Name must be 1-80 characters")
		return
	}
	if err := h.store.RenameDevice(dev.ID, name); err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	dev.Name = name
	WriteJSON(w, http.StatusOK, dev)
}

// DeleteDevice handles DELETE /api/v1/devices/{id}
func (h *APIHandler) DeleteDevice(w http.ResponseWriter, r *http.Request) {
	dev := h.ownedDevice(w, r)
	if dev == nil {
		return
	}
	if err := h.store.DeleteDevice(dev.ID); err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	h.hub.Disconnect(dev.ID)
	WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// Webhook Handlers
func (h *APIHandler) ListWebhooks(w http.ResponseWriter, r *http.Request) {
	whs, err := h.store.ListWebhooks(principal(r).Owner())
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
	if !decodeJSON(w, r, &req) {
		return
	}
	req.URL = strings.TrimSpace(req.URL)
	if !strings.HasPrefix(req.URL, "http://") && !strings.HasPrefix(req.URL, "https://") {
		WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "URL must start with http:// or https://")
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
		UserID:    principal(r).UserID(),
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

func (h *APIHandler) ownedWebhook(w http.ResponseWriter, r *http.Request) *domain.Webhook {
	wh, err := h.store.GetWebhook(chi.URLParam(r, "id"))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return nil
	}
	if wh == nil || !principal(r).CanAccess(wh.UserID) {
		notFound(w, "Webhook")
		return nil
	}
	return wh
}

func (h *APIHandler) DeleteWebhook(w http.ResponseWriter, r *http.Request) {
	wh := h.ownedWebhook(w, r)
	if wh == nil {
		return
	}
	if err := h.store.DeleteWebhook(wh.ID); err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (h *APIHandler) TestWebhook(w http.ResponseWriter, r *http.Request) {
	wh := h.ownedWebhook(w, r)
	if wh == nil {
		return
	}
	delivery, err := h.dispatcher.TestWebhook(wh)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "DELIVERY_ERROR", err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, delivery)
}

func (h *APIHandler) ListWebhookDeliveries(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	deliveries, err := h.store.ListWebhookDeliveries(principal(r).Owner(), r.URL.Query().Get("webhook_id"), limit)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, deliveries)
}

// API Token Handlers: tokens always belong to (and act as) the user who made them.
func (h *APIHandler) ListTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := h.store.ListApiTokens(principal(r).Owner())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, tokens)
}

var validScopes = map[string]bool{
	"*": true, domain.ScopeSMSSend: true, domain.ScopeSMSRead: true, domain.ScopeDevicesManage: true,
	domain.ScopeWebhooksManage: true, domain.ScopeOTAManage: true, domain.ScopeWorkflows: true,
}

func (h *APIHandler) CreateToken(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if p.User == nil {
		WriteError(w, http.StatusBadRequest, "NO_ACCOUNT", "Create a dashboard account before issuing API tokens")
		return
	}
	var req struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		req.Name = "API token"
	}
	if len(req.Scopes) == 0 {
		req.Scopes = []string{domain.ScopeSMSSend, domain.ScopeSMSRead}
	}
	for _, s := range req.Scopes {
		if !validScopes[s] {
			WriteError(w, http.StatusBadRequest, "VALIDATION_FAILED", "Unknown scope: "+s)
			return
		}
	}

	rawToken := middleware.NewRandomToken("htsms_live_", 24)
	hBytes := sha256.Sum256([]byte(rawToken))
	token := domain.ApiToken{
		ID:          uuid.New().String(),
		UserID:      p.User.ID,
		Name:        req.Name,
		TokenPrefix: rawToken[:16] + "...",
		TokenHash:   hex.EncodeToString(hBytes[:]),
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
	tok, err := h.store.GetApiToken(chi.URLParam(r, "id"))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	if tok == nil || !principal(r).CanAccess(tok.UserID) {
		notFound(w, "Token")
		return
	}
	if err := h.store.DeleteApiToken(tok.ID); err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// OTA Update Handlers (public check/download, admin upload)
func (h *APIHandler) CheckOtaUpdate(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Query().Get("target")
	if target == "" {
		target = "android"
	}
	currentCode, _ := strconv.Atoi(r.URL.Query().Get("version_code"))

	release, err := h.store.GetLatestOtaRelease(target)
	if err != nil || release == nil {
		WriteJSON(w, http.StatusOK, map[string]interface{}{"update_available": false})
		return
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"update_available": release.VersionCode > currentCode,
		"release":          release,
		"download_url":     fmt.Sprintf("/api/v1/ota/download/%s", release.ID),
	})
}

func (h *APIHandler) ListOtaReleases(w http.ResponseWriter, r *http.Request) {
	releases, err := h.store.ListOtaReleases()
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, releases)
}

func (h *APIHandler) UploadOtaRelease(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 200<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		WriteError(w, http.StatusBadRequest, "FILE_TOO_LARGE", "Max upload size is 200MB")
		return
	}

	target := r.FormValue("target")
	if target == "" {
		target = "android"
	}
	version := strings.TrimSpace(r.FormValue("version"))
	versionCode, err := strconv.Atoi(r.FormValue("version_code"))
	if version == "" || err != nil || versionCode <= 0 {
		WriteError(w, http.StatusBadRequest, "VALIDATION_FAILED", "version and a positive version_code are required")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		WriteError(w, http.StatusBadRequest, "FILE_REQUIRED", "No file uploaded")
		return
	}
	defer file.Close()

	releaseID := uuid.New().String()
	destPath := filepath.Join(h.otaDir, fmt.Sprintf("%s_%s_%s", target, releaseID, filepath.Base(header.Filename)))
	destFile, err := os.Create(destPath)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "STORAGE_ERROR", err.Error())
		return
	}
	defer destFile.Close()

	hasher := sha256.New()
	fileSize, err := io.Copy(io.MultiWriter(destFile, hasher), file)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "WRITE_ERROR", err.Error())
		return
	}

	release := domain.OtaRelease{
		ID:             releaseID,
		Target:         target,
		Version:        version,
		VersionCode:    versionCode,
		FileName:       header.Filename,
		FilePath:       destPath,
		ChecksumSHA256: hex.EncodeToString(hasher.Sum(nil)),
		FileSize:       fileSize,
		Changelog:      r.FormValue("changelog"),
		IsActive:       true,
		CreatedAt:      time.Now().UTC(),
	}
	if err := h.store.SaveOtaRelease(&release); err != nil {
		WriteError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	h.hub.BroadcastToDashboards("", "ota.new_release", release)
	WriteJSON(w, http.StatusCreated, release)
}

func (h *APIHandler) DownloadOtaRelease(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	releases, err := h.store.ListOtaReleases()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	for _, rel := range releases {
		if rel.ID == id && rel.FilePath != "" {
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", rel.FileName))
			w.Header().Set("X-Checksum-SHA256", rel.ChecksumSHA256)
			http.ServeFile(w, r, rel.FilePath)
			return
		}
	}
	http.NotFound(w, r)
}

// GetSystemStats handles GET /api/v1/system/stats (scoped to the caller)
func (h *APIHandler) GetSystemStats(w http.ResponseWriter, r *http.Request) {
	owner := principal(r).Owner()
	devices, _ := h.store.ListDevices(owner)
	online := 0
	for _, d := range devices {
		if d.Status == "online" {
			online++
		}
	}
	msgStats, _ := h.store.GetMessageStats(owner)
	webhooks, _ := h.store.ListWebhooks(owner)
	workflows, _ := h.store.ListWorkflows(owner, false)
	activeWorkflows := 0
	for _, wf := range workflows {
		if wf.Enabled {
			activeWorkflows++
		}
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"online_devices":   online,
		"total_devices":    len(devices),
		"total_inbound":    msgStats.Inbound,
		"total_outbound":   msgStats.Outbound,
		"total_failed":     msgStats.Failed,
		"inbound_24h":      msgStats.InboundToday,
		"outbound_24h":     msgStats.SentToday,
		"active_webhooks":  len(webhooks),
		"active_workflows": activeWorkflows,
		"total_workflows":  len(workflows),
		"timestamp":        time.Now().UTC(),
	})
}
