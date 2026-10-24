package ws

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/httpsms/gateway-server/internal/api/middleware"
	"github.com/httpsms/gateway-server/internal/domain"
	"github.com/httpsms/gateway-server/internal/storage"
	"github.com/httpsms/gateway-server/internal/webhook"
)

// Phones are native clients without an Origin header, so any origin is fine.
var deviceUpgrader = websocket.Upgrader{
	CheckOrigin:     func(r *http.Request) bool { return true },
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
}

// Dashboards authenticate with a cookie, so only same-origin pages may connect;
// otherwise any website could open a socket with the user's cookie attached.
var dashboardUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		u, err := url.Parse(origin)
		return err == nil && strings.EqualFold(u.Host, r.Host)
	},
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
}

// InboundHook runs for every new (non-duplicate) inbound SMS, e.g. to start workflows.
type InboundHook func(msg domain.Message)

// Authenticator resolves the signed-in user of a dashboard socket request.
type Authenticator func(r *http.Request) (*middleware.Principal, error)

// Frame represents a JSON message exchanged over the WebSocket connection
type Frame struct {
	Action         string          `json:"action"` // e.g., "device.heartbeat", "sms.received", "sms.send", "sms.status", "ack"
	Data           json.RawMessage `json:"data,omitempty"`
	IdempotencyKey string          `json:"idempotency_key,omitempty"`
	Error          string          `json:"error,omitempty"`
}

// DeviceConn represents an active connection to an Android phone
type DeviceConn struct {
	DeviceID string
	OwnerID  string
	Conn     *websocket.Conn
	SendChan chan []byte
	Hub      *Hub
}

// DashboardConn represents an active connection to a browser UI
type DashboardConn struct {
	ID       string
	UserID   string
	IsAdmin  bool
	Conn     *websocket.Conn
	SendChan chan []byte
	Hub      *Hub
}

type Hub struct {
	store      *storage.DB
	dispatcher *webhook.Dispatcher

	devices    map[string]*DeviceConn
	dashboards map[string]*DashboardConn

	registerDevice   chan *DeviceConn
	unregisterDevice chan *DeviceConn

	registerDash   chan *DashboardConn
	unregisterDash chan *DashboardConn

	broadcastDash chan dashEvent
	mu            sync.RWMutex

	auth      Authenticator
	onInbound InboundHook
}

type dashEvent struct {
	owner   string
	payload []byte
}

// SetAuthenticator and SetInboundHook complete wiring after construction.
func (h *Hub) SetAuthenticator(a Authenticator) { h.auth = a }
func (h *Hub) SetInboundHook(fn InboundHook)    { h.onInbound = fn }

func NewHub(store *storage.DB, dispatcher *webhook.Dispatcher) *Hub {
	return &Hub{
		store:            store,
		dispatcher:       dispatcher,
		devices:          make(map[string]*DeviceConn),
		dashboards:       make(map[string]*DashboardConn),
		registerDevice:   make(chan *DeviceConn),
		unregisterDevice: make(chan *DeviceConn),
		registerDash:     make(chan *DashboardConn),
		unregisterDash:   make(chan *DashboardConn),
		broadcastDash:    make(chan dashEvent, 256),
	}
}

func (h *Hub) Run() {
	for {
		select {
		case dev := <-h.registerDevice:
			h.mu.Lock()
			// If existing connection exists, close it
			if old, ok := h.devices[dev.DeviceID]; ok {
				close(old.SendChan)
				old.Conn.Close()
			}
			h.devices[dev.DeviceID] = dev
			h.mu.Unlock()
			log.Printf("[WebSocket] Android device connected: %s", dev.DeviceID)
			h.broadcastDeviceStatus(dev.DeviceID, "online")

		case dev := <-h.unregisterDevice:
			h.mu.Lock()
			removed := false
			if curr, ok := h.devices[dev.DeviceID]; ok && curr == dev {
				delete(h.devices, dev.DeviceID)
				close(dev.SendChan)
				removed = true
			}
			h.mu.Unlock()
			// A superseded connection (phone reconnected) must not mark the device offline.
			if removed {
				log.Printf("[WebSocket] Android device disconnected: %s", dev.DeviceID)
				h.broadcastDeviceStatus(dev.DeviceID, "offline")
			}

		case dash := <-h.registerDash:
			h.mu.Lock()
			h.dashboards[dash.ID] = dash
			h.mu.Unlock()

		case dash := <-h.unregisterDash:
			h.mu.Lock()
			if _, ok := h.dashboards[dash.ID]; ok {
				delete(h.dashboards, dash.ID)
				close(dash.SendChan)
			}
			h.mu.Unlock()

		case ev := <-h.broadcastDash:
			h.mu.RLock()
			for _, dash := range h.dashboards {
				if !dash.IsAdmin && dash.UserID != ev.owner {
					continue
				}
				select {
				case dash.SendChan <- ev.payload:
				default:
					// Slow client, skip frame
				}
			}
			h.mu.RUnlock()
		}
	}
}

// BroadcastToDashboards sends an event to the owner's dashboards and to admins.
func (h *Hub) BroadcastToDashboards(owner, eventType string, data interface{}) {
	payload, err := json.Marshal(map[string]interface{}{
		"event": eventType,
		"data":  data,
		"time":  time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return
	}
	select {
	case h.broadcastDash <- dashEvent{owner: owner, payload: payload}:
	default:
	}
}

func (h *Hub) broadcastDeviceStatus(deviceID, status string) {
	// Update device status in DB
	d, err := h.store.GetDevice(deviceID)
	if err != nil || d == nil {
		return
	}
	d.Status = status
	_ = h.store.UpsertDevice(d)

	h.BroadcastToDashboards(d.UserID, "device.status", map[string]string{
		"device_id": deviceID,
		"status":    status,
	})
}

// SendSmsCommand delivers an outbound SMS sending job to an online Android device
func (h *Hub) SendSmsCommand(msg *domain.Message) error {
	h.mu.RLock()
	devConn, ok := h.devices[msg.DeviceID]
	if ok && devConn.OwnerID != msg.UserID {
		ok = false
	}
	if !ok && msg.DeviceID == "" {
		// Auto-route through any online phone belonging to the same user.
		for _, c := range h.devices {
			if c.OwnerID == msg.UserID {
				devConn, ok = c, true
				msg.DeviceID = c.DeviceID
				break
			}
		}
	}
	h.mu.RUnlock()

	if !ok || devConn == nil {
		return ErrDeviceOffline
	}

	frameData, _ := json.Marshal(map[string]interface{}{
		"id":          msg.ID,
		"recipient":   msg.Recipient,
		"body":        msg.Body,
		"sim_slot":    msg.SimSlot,
		"parts_count": msg.PartsCount,
	})

	frame := Frame{
		Action:         "sms.send",
		Data:           frameData,
		IdempotencyKey: msg.IdempotencyKey,
	}

	frameBytes, _ := json.Marshal(frame)
	if !devConn.trySend(frameBytes) {
		return ErrDeviceBusy
	}
	_ = h.store.SetMessageDevice(msg.ID, msg.DeviceID)
	_ = h.store.UpdateMessageStatus(msg.ID, domain.MessageStatusDispatched, "", nil, nil)
	msg.Status = domain.MessageStatusDispatched
	h.BroadcastToDashboards(msg.UserID, "message.dispatched", msg)
	return nil
}

// IsOnline reports whether a device currently has a live connection.
func (h *Hub) IsOnline(deviceID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.devices[deviceID]
	return ok
}

// Disconnect drops a device's socket, e.g. after it is unpaired.
func (h *Hub) Disconnect(deviceID string) {
	h.mu.RLock()
	c, ok := h.devices[deviceID]
	h.mu.RUnlock()
	if ok {
		_ = c.Conn.Close()
	}
}

// trySend queues a frame without blocking. The hub closes SendChan when a
// connection is replaced or dropped, so a send can race with that close;
// treat it as a failed send instead of crashing the server.
func (c *DeviceConn) trySend(frame []byte) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	select {
	case c.SendChan <- frame:
		return true
	default:
		return false
	}
}

var (
	ErrDeviceOffline = &CustomError{Code: "DEVICE_OFFLINE", Message: "Gateway Android device is offline"}
	ErrDeviceBusy    = &CustomError{Code: "DEVICE_BUSY", Message: "Gateway Android device queue is busy"}
)

type CustomError struct {
	Code    string
	Message string
}

func (e *CustomError) Error() string {
	return e.Message
}

// HandleDeviceWs handles the WebSocket connection from an Android phone
func (h *Hub) HandleDeviceWs(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		token = r.Header.Get("X-Device-Token")
	}

	if token == "" {
		http.Error(w, "Device authentication token required", http.StatusUnauthorized)
		return
	}

	th := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(th[:])

	device, err := h.store.GetDeviceByTokenHash(tokenHash)
	if err != nil || device == nil {
		http.Error(w, "Invalid device token", http.StatusUnauthorized)
		return
	}

	conn, err := deviceUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[WebSocket] Upgrade error: %v", err)
		return
	}

	devConn := &DeviceConn{
		DeviceID: device.ID,
		OwnerID:  device.UserID,
		Conn:     conn,
		SendChan: make(chan []byte, 128),
		Hub:      h,
	}

	h.registerDevice <- devConn

	go devConn.writePump()
	devConn.readPump()
}

// HandleDashboardWs handles the WebSocket connection from the web dashboard
func (h *Hub) HandleDashboardWs(w http.ResponseWriter, r *http.Request) {
	if h.auth == nil {
		http.Error(w, "dashboard auth not configured", http.StatusInternalServerError)
		return
	}
	p, err := h.auth(r)
	if err != nil || p == nil {
		http.Error(w, "Sign in required", http.StatusUnauthorized)
		return
	}

	conn, err := dashboardUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	dash := &DashboardConn{
		ID:       uuid.New().String(),
		UserID:   p.UserID(),
		IsAdmin:  p.IsAdmin(),
		Conn:     conn,
		SendChan: make(chan []byte, 128),
		Hub:      h,
	}

	h.registerDash <- dash

	go dash.writePump()
	dash.readPump()
}

func (c *DeviceConn) readPump() {
	defer func() {
		c.Hub.unregisterDevice <- c
		c.Conn.Close()
	}()

	c.Conn.SetReadLimit(65536)
	_ = c.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.Conn.SetPongHandler(func(string) error {
		_ = c.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, message, err := c.Conn.ReadMessage()
		if err != nil {
			break
		}

		var frame Frame
		if err := json.Unmarshal(message, &frame); err != nil {
			continue
		}

		c.handleIncomingFrame(&frame)
	}
}

func (c *DeviceConn) writePump() {
	ticker := time.NewTicker(25 * time.Second)
	defer func() {
		ticker.Stop()
		c.Conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.SendChan:
			_ = c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				_ = c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.Conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}

		case <-ticker.C:
			_ = c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (c *DeviceConn) handleIncomingFrame(frame *Frame) {
	switch frame.Action {
	case "device.heartbeat", "device.register":
		var payload struct {
			BatteryLevel   int              `json:"battery_level"`
			IsCharging     bool             `json:"is_charging"`
			AppVersion     string           `json:"app_version"`
			AppVersionCode int              `json:"app_version_code"`
			SimCards       []domain.SimCard `json:"sim_cards"`
		}
		if err := json.Unmarshal(frame.Data, &payload); err == nil {
			dev, _ := c.Hub.store.GetDevice(c.DeviceID)
			if dev != nil {
				dev.Status = "online"
				dev.BatteryLevel = payload.BatteryLevel
				dev.IsCharging = payload.IsCharging
				dev.AppVersion = payload.AppVersion
				dev.AppVersionCode = payload.AppVersionCode
				dev.LastSeenAt = time.Now().UTC()
				_ = c.Hub.store.UpsertDevice(dev)

				if len(payload.SimCards) > 0 {
					if err := c.Hub.store.ReplaceDeviceSimCards(c.DeviceID, payload.SimCards); err != nil {
						log.Printf("[WebSocket] Failed to store SIM cards for %s: %v", c.DeviceID, err)
					}
					if fresh, _ := c.Hub.store.GetDevice(c.DeviceID); fresh != nil {
						dev = fresh
					}
				}
				c.Hub.BroadcastToDashboards(dev.UserID, "device.heartbeat", dev)
			}
		}

	case "sms.received":
		var payload struct {
			Sender         string `json:"sender"`
			Recipient      string `json:"recipient"`
			Body           string `json:"body"`
			SimSlot        int    `json:"sim_slot"`
			SimCardID      string `json:"sim_card_id"`
			PartsCount     int    `json:"parts_count"`
			IdempotencyKey string `json:"idempotency_key"`
		}
		if err := json.Unmarshal(frame.Data, &payload); err == nil {
			msgID := uuid.New().String()
			msg := domain.Message{
				ID:             msgID,
				UserID:         c.OwnerID,
				Direction:      domain.DirectionInbound,
				DeviceID:       c.DeviceID,
				SimSlot:        payload.SimSlot,
				SimCardID:      payload.SimCardID,
				Sender:         payload.Sender,
				Recipient:      payload.Recipient,
				Body:           payload.Body,
				Status:         domain.MessageStatusReceived,
				IdempotencyKey: payload.IdempotencyKey,
				PartsCount:     payload.PartsCount,
				CreatedAt:      time.Now().UTC(),
			}

			isNew, err := c.Hub.store.SaveMessage(&msg)
			if err == nil {
				// Send ACK back to Android device so it can clear its local outbox
				ackFrame, _ := json.Marshal(Frame{
					Action:         "ack",
					IdempotencyKey: payload.IdempotencyKey,
				})
				c.trySend(ackFrame)

				// ONLY dispatch webhooks and dashboard event if it's NOT a duplicate!
				if isNew {
					log.Printf("[SMS] Received unique incoming message from %s: %s (id: %s)", msg.Sender, msg.Body, msg.ID)
					c.Hub.BroadcastToDashboards(c.OwnerID, "sms.received", msg)
					_ = c.Hub.dispatcher.Dispatch(c.OwnerID, domain.EventSMSReceived, &msg.ID, msg)
					if c.Hub.onInbound != nil {
						c.Hub.onInbound(msg)
					}
				} else {
					log.Printf("[SMS] Duplicate incoming message discarded (key: %s)", payload.IdempotencyKey)
				}
			}
		}

	case "sms.status":
		var payload struct {
			MessageID    string `json:"message_id"`
			Status       string `json:"status"` // sent, delivered, failed
			ErrorMessage string `json:"error_message"`
		}
		if err := json.Unmarshal(frame.Data, &payload); err == nil {
			now := time.Now().UTC()
			var sentAt, deliveredAt *time.Time
			if payload.Status == domain.MessageStatusSent {
				sentAt = &now
			} else if payload.Status == domain.MessageStatusDelivered {
				deliveredAt = &now
			}

			msg, _ := c.Hub.store.GetMessage(payload.MessageID)
			if msg == nil || msg.UserID != c.OwnerID {
				break // a phone may only report on its owner's messages
			}
			_ = c.Hub.store.UpdateMessageStatus(payload.MessageID, payload.Status, payload.ErrorMessage, sentAt, deliveredAt)
			msg, _ = c.Hub.store.GetMessage(payload.MessageID)

			c.Hub.BroadcastToDashboards(c.OwnerID, "sms.status", map[string]interface{}{
				"message_id": payload.MessageID,
				"status":     payload.Status,
				"error":      payload.ErrorMessage,
			})

			// Dispatch webhook for status update
			eventType := domain.EventSMSSent
			if payload.Status == domain.MessageStatusDelivered {
				eventType = domain.EventSMSDelivered
			} else if payload.Status == domain.MessageStatusFailed {
				eventType = domain.EventSMSFailed
			}

			if msg != nil {
				_ = c.Hub.dispatcher.Dispatch(c.OwnerID, eventType, &payload.MessageID, msg)
			}
		}
	}
}

func (c *DashboardConn) readPump() {
	defer func() {
		c.Hub.unregisterDash <- c
		c.Conn.Close()
	}()
	c.Conn.SetReadLimit(4096)
	_ = c.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.Conn.SetPongHandler(func(string) error {
		_ = c.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})
	for {
		_, _, err := c.Conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

func (c *DashboardConn) writePump() {
	ticker := time.NewTicker(25 * time.Second)
	defer func() {
		ticker.Stop()
		c.Conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.SendChan:
			_ = c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				_ = c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.Conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
