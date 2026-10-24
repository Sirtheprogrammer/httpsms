package domain

import (
	"time"
)

// Message status constants
const (
	MessageStatusQueued     = "queued"
	MessageStatusDispatched = "dispatched"
	MessageStatusSent       = "sent"
	MessageStatusDelivered  = "delivered"
	MessageStatusFailed     = "failed"
	MessageStatusReceived   = "received"

	DirectionInbound  = "inbound"
	DirectionOutbound = "outbound"
)

// Webhook event types
const (
	EventSMSReceived     = "sms.received"
	EventSMSSent         = "sms.sent"
	EventSMSDelivered    = "sms.delivered"
	EventSMSFailed       = "sms.failed"
	EventDeviceHeartbeat = "device.heartbeat"
	EventDeviceOffline   = "device.offline"
)

// Scopes for API tokens
const (
	ScopeSMSSend       = "sms:send"
	ScopeSMSRead       = "sms:read"
	ScopeDevicesManage = "devices:manage"
	ScopeWebhooksManage= "webhooks:manage"
	ScopeOTAManage     = "ota:manage"
)

// Device represents an Android gateway phone
type Device struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	TokenHash      string    `json:"-"`
	Status         string    `json:"status"` // online, offline, busy
	BatteryLevel   int       `json:"battery_level"`
	IsCharging     bool      `json:"is_charging"`
	AppVersion     string    `json:"app_version"`
	AppVersionCode int       `json:"app_version_code"`
	LastSeenAt     time.Time `json:"last_seen_at"`
	CreatedAt      time.Time `json:"created_at"`
	SimCards       []SimCard `json:"sim_cards,omitempty"`
}

// SimCard represents a SIM slot in an Android device
type SimCard struct {
	ID          string `json:"id"`
	DeviceID    string `json:"device_id"`
	SlotIndex   int    `json:"slot_index"` // 0 or 1
	SubID       int    `json:"sub_id"`     // Android Subscription ID
	CarrierName string `json:"carrier_name"`
	PhoneNumber string `json:"phone_number"`
	CountryISO  string `json:"country_iso"`
	SignalLevel int    `json:"signal_level"` // 0 to 4 bars
	SignalDbm   int    `json:"signal_dbm"`
	IsActive    bool   `json:"is_active"`
}

// Message represents an incoming or outgoing SMS
type Message struct {
	ID             string     `json:"id"`
	Direction      string     `json:"direction"` // inbound or outbound
	DeviceID       string     `json:"device_id"`
	SimCardID      string     `json:"sim_card_id,omitempty"`
	SimSlot        int        `json:"sim_slot"`
	Sender         string     `json:"sender"`
	Recipient      string     `json:"recipient"`
	Body           string     `json:"body"`
	Status         string     `json:"status"`
	IdempotencyKey string     `json:"idempotency_key"`
	PartsCount     int        `json:"parts_count"`
	ErrorMessage   string     `json:"error_message,omitempty"`
	ScheduledAt    *time.Time `json:"scheduled_at,omitempty"`
	SentAt         *time.Time `json:"sent_at,omitempty"`
	DeliveredAt    *time.Time `json:"delivered_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

// Webhook represents an endpoint configured to receive SMS events
type Webhook struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	Secret    string    `json:"secret,omitempty"` // For HMAC-SHA256 signing
	Events    []string  `json:"events"`           // Subscribed event types
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
}

// WebhookDelivery tracks an attempt to deliver an event to a webhook endpoint
type WebhookDelivery struct {
	ID             string     `json:"id"`
	WebhookID      string     `json:"webhook_id"`
	MessageID      *string    `json:"message_id,omitempty"`
	EventType      string     `json:"event_type"`
	Payload        string     `json:"payload"`
	ResponseStatus int        `json:"response_status"`
	ResponseBody   string     `json:"response_body"`
	LatencyMs      int64      `json:"latency_ms"`
	Attempt        int        `json:"attempt"`
	Status         string     `json:"status"` // success, failed, retrying
	NextRetryAt    *time.Time `json:"next_retry_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

// ApiToken represents a consumer API authentication key
type ApiToken struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	TokenPrefix string     `json:"token_prefix"` // e.g., "htsms_live_a1b2..."
	TokenHash   string     `json:"-"`
	Scopes      []string   `json:"scopes"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// OtaRelease represents an uploaded APK or UI bundle
type OtaRelease struct {
	ID             string    `json:"id"`
	Target         string    `json:"target"` // android or ui
	Version        string    `json:"version"`
	VersionCode    int       `json:"version_code"`
	FileName       string    `json:"file_name"`
	FilePath       string    `json:"-"`
	ChecksumSHA256 string    `json:"checksum_sha256"`
	FileSize       int64     `json:"file_size"`
	Changelog      string    `json:"changelog"`
	IsActive       bool      `json:"is_active"`
	CreatedAt      time.Time `json:"created_at"`
}

