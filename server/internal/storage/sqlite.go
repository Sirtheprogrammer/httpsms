package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/httpsms/gateway-server/internal/domain"
	_ "modernc.org/sqlite"
)

type DB struct {
	db *sql.DB
	mu sync.RWMutex
}

// NewDB initializes SQLite with WAL mode, busy timeout, and schema migrations
func NewDB(dbPath string) (*DB, error) {
	dir := filepath.Dir(dbPath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create db directory: %w", err)
		}
	}

	dsn := fmt.Sprintf("%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	// Recommended connection pooling for SQLite with WAL
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(time.Hour)

	store := &DB{db: db}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("database migration failed: %w", err)
	}

	return store, nil
}

func (s *DB) Close() error {
	return s.db.Close()
}

func (s *DB) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS devices (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		token_hash TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'offline',
		battery_level INTEGER DEFAULT 0,
		is_charging INTEGER DEFAULT 0,
		app_version TEXT DEFAULT '',
		app_version_code INTEGER DEFAULT 0,
		last_seen_at DATETIME,
		created_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS sim_cards (
		id TEXT PRIMARY KEY,
		device_id TEXT NOT NULL,
		slot_index INTEGER NOT NULL,
		sub_id INTEGER NOT NULL,
		carrier_name TEXT DEFAULT '',
		phone_number TEXT DEFAULT '',
		country_iso TEXT DEFAULT '',
		signal_level INTEGER DEFAULT 0,
		signal_dbm INTEGER DEFAULT 0,
		is_active INTEGER DEFAULT 1,
		FOREIGN KEY(device_id) REFERENCES devices(id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS messages (
		id TEXT PRIMARY KEY,
		direction TEXT NOT NULL,
		device_id TEXT NOT NULL,
		sim_card_id TEXT DEFAULT '',
		sim_slot INTEGER DEFAULT 0,
		sender TEXT NOT NULL,
		recipient TEXT NOT NULL,
		body TEXT NOT NULL,
		status TEXT NOT NULL,
		idempotency_key TEXT UNIQUE,
		parts_count INTEGER DEFAULT 1,
		error_message TEXT DEFAULT '',
		scheduled_at DATETIME,
		sent_at DATETIME,
		delivered_at DATETIME,
		created_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS webhooks (
		id TEXT PRIMARY KEY,
		url TEXT NOT NULL,
		secret TEXT NOT NULL,
		events TEXT NOT NULL,
		is_active INTEGER DEFAULT 1,
		created_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS webhook_deliveries (
		id TEXT PRIMARY KEY,
		webhook_id TEXT NOT NULL,
		message_id TEXT,
		event_type TEXT NOT NULL,
		payload TEXT NOT NULL,
		response_status INTEGER DEFAULT 0,
		response_body TEXT DEFAULT '',
		latency_ms INTEGER DEFAULT 0,
		attempt INTEGER DEFAULT 1,
		status TEXT NOT NULL,
		next_retry_at DATETIME,
		created_at DATETIME NOT NULL,
		FOREIGN KEY(webhook_id) REFERENCES webhooks(id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS api_tokens (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		token_prefix TEXT NOT NULL,
		token_hash TEXT UNIQUE NOT NULL,
		scopes TEXT NOT NULL,
		last_used_at DATETIME,
		expires_at DATETIME,
		created_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS ota_releases (
		id TEXT PRIMARY KEY,
		target TEXT NOT NULL,
		version TEXT NOT NULL,
		version_code INTEGER NOT NULL,
		file_name TEXT NOT NULL,
		file_path TEXT NOT NULL,
		checksum_sha256 TEXT NOT NULL,
		file_size INTEGER NOT NULL,
		changelog TEXT DEFAULT '',
		is_active INTEGER DEFAULT 1,
		created_at DATETIME NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_messages_idempotency ON messages(idempotency_key);
	CREATE INDEX IF NOT EXISTS idx_messages_created_at ON messages(created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_messages_direction ON messages(direction);
	CREATE INDEX IF NOT EXISTS idx_messages_status ON messages(status);
	CREATE INDEX IF NOT EXISTS idx_sim_cards_device ON sim_cards(device_id);
	CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_webhook ON webhook_deliveries(webhook_id);
	CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_status ON webhook_deliveries(status);
	`
	_, err := s.db.Exec(schema)
	return err
}

// Device operations
func (s *DB) UpsertDevice(d *domain.Device) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `
	INSERT INTO devices (id, name, token_hash, status, battery_level, is_charging, app_version, app_version_code, last_seen_at, created_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		name = excluded.name,
		status = excluded.status,
		battery_level = excluded.battery_level,
		is_charging = excluded.is_charging,
		app_version = excluded.app_version,
		app_version_code = excluded.app_version_code,
		last_seen_at = excluded.last_seen_at;
	`
	isCharging := 0
	if d.IsCharging {
		isCharging = 1
	}

	_, err := s.db.Exec(query, d.ID, d.Name, d.TokenHash, d.Status, d.BatteryLevel, isCharging, d.AppVersion, d.AppVersionCode, d.LastSeenAt, d.CreatedAt)
	return err
}

func (s *DB) GetDevice(id string) (*domain.Device, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT id, name, token_hash, status, battery_level, is_charging, app_version, app_version_code, last_seen_at, created_at FROM devices WHERE id = ?`
	row := s.db.QueryRow(query, id)

	var d domain.Device
	var isCharging int
	err := row.Scan(&d.ID, &d.Name, &d.TokenHash, &d.Status, &d.BatteryLevel, &isCharging, &d.AppVersion, &d.AppVersionCode, &d.LastSeenAt, &d.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d.IsCharging = isCharging == 1

	// Fetch SIM cards
	sims, err := s.GetDeviceSimCards(d.ID)
	if err == nil {
		d.SimCards = sims
	}

	return &d, nil
}

func (s *DB) GetDeviceByTokenHash(tokenHash string) (*domain.Device, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT id, name, token_hash, status, battery_level, is_charging, app_version, app_version_code, last_seen_at, created_at FROM devices WHERE token_hash = ?`
	row := s.db.QueryRow(query, tokenHash)

	var d domain.Device
	var isCharging int
	err := row.Scan(&d.ID, &d.Name, &d.TokenHash, &d.Status, &d.BatteryLevel, &isCharging, &d.AppVersion, &d.AppVersionCode, &d.LastSeenAt, &d.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d.IsCharging = isCharging == 1
	sims, err := s.GetDeviceSimCards(d.ID)
	if err == nil {
		d.SimCards = sims
	}
	return &d, nil
}

func (s *DB) ListDevices() ([]domain.Device, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT id, name, token_hash, status, battery_level, is_charging, app_version, app_version_code, last_seen_at, created_at FROM devices ORDER BY last_seen_at DESC`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var devices []domain.Device
	for rows.Next() {
		var d domain.Device
		var isCharging int
		if err := rows.Scan(&d.ID, &d.Name, &d.TokenHash, &d.Status, &d.BatteryLevel, &isCharging, &d.AppVersion, &d.AppVersionCode, &d.LastSeenAt, &d.CreatedAt); err != nil {
			return nil, err
		}
		d.IsCharging = isCharging == 1
		sims, err := s.GetDeviceSimCards(d.ID)
		if err == nil {
			d.SimCards = sims
		}
		devices = append(devices, d)
	}
	return devices, nil
}

func (s *DB) DeleteDevice(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("DELETE FROM devices WHERE id = ?", id)
	return err
}

// SimCard operations
func (s *DB) ReplaceDeviceSimCards(deviceID string, sims []domain.SimCard) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM sim_cards WHERE device_id = ?", deviceID); err != nil {
		return err
	}

	stmt, err := tx.Prepare(`INSERT INTO sim_cards (id, device_id, slot_index, sub_id, carrier_name, phone_number, country_iso, signal_level, signal_dbm, is_active) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, sim := range sims {
		isActive := 0
		if sim.IsActive {
			isActive = 1
		}
		if _, err := stmt.Exec(sim.ID, deviceID, sim.SlotIndex, sim.SubID, sim.CarrierName, sim.PhoneNumber, sim.CountryISO, sim.SignalLevel, sim.SignalDbm, isActive); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *DB) GetDeviceSimCards(deviceID string) ([]domain.SimCard, error) {
	query := `SELECT id, device_id, slot_index, sub_id, carrier_name, phone_number, country_iso, signal_level, signal_dbm, is_active FROM sim_cards WHERE device_id = ? ORDER BY slot_index ASC`
	rows, err := s.db.Query(query, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sims []domain.SimCard
	for rows.Next() {
		var sim domain.SimCard
		var isActive int
		if err := rows.Scan(&sim.ID, &sim.DeviceID, &sim.SlotIndex, &sim.SubID, &sim.CarrierName, &sim.PhoneNumber, &sim.CountryISO, &sim.SignalLevel, &sim.SignalDbm, &isActive); err != nil {
			return nil, err
		}
		sim.IsActive = isActive == 1
		sims = append(sims, sim)
	}
	return sims, nil
}

// Message operations with exact deduplication
func (s *DB) SaveMessage(m *domain.Message) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check idempotency key for deduplication
	if m.IdempotencyKey != "" {
		var existingID string
		err := s.db.QueryRow("SELECT id FROM messages WHERE idempotency_key = ?", m.IdempotencyKey).Scan(&existingID)
		if err == nil {
			// Found duplicate!
			m.ID = existingID
			return false, nil // false indicates already existed / was duplicate
		}
	}

	query := `
	INSERT INTO messages (id, direction, device_id, sim_card_id, sim_slot, sender, recipient, body, status, idempotency_key, parts_count, error_message, scheduled_at, sent_at, delivered_at, created_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := s.db.Exec(query, m.ID, m.Direction, m.DeviceID, m.SimCardID, m.SimSlot, m.Sender, m.Recipient, m.Body, m.Status, m.IdempotencyKey, m.PartsCount, m.ErrorMessage, m.ScheduledAt, m.SentAt, m.DeliveredAt, m.CreatedAt)
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *DB) UpdateMessageStatus(id, status, errorMsg string, sentAt, deliveredAt *time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `UPDATE messages SET status = ?, error_message = ?, sent_at = COALESCE(?, sent_at), delivered_at = COALESCE(?, delivered_at) WHERE id = ?`
	_, err := s.db.Exec(query, status, errorMsg, sentAt, deliveredAt, id)
	return err
}

func (s *DB) GetMessage(id string) (*domain.Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT id, direction, device_id, sim_card_id, sim_slot, sender, recipient, body, status, idempotency_key, parts_count, error_message, scheduled_at, sent_at, delivered_at, created_at FROM messages WHERE id = ?`
	row := s.db.QueryRow(query, id)

	var m domain.Message
	err := row.Scan(&m.ID, &m.Direction, &m.DeviceID, &m.SimCardID, &m.SimSlot, &m.Sender, &m.Recipient, &m.Body, &m.Status, &m.IdempotencyKey, &m.PartsCount, &m.ErrorMessage, &m.ScheduledAt, &m.SentAt, &m.DeliveredAt, &m.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &m, err
}

func (s *DB) ListMessages(direction, search string, limit, offset int) ([]domain.Message, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var whereClauses []string
	var args []interface{}

	if direction != "" {
		whereClauses = append(whereClauses, "direction = ?")
		args = append(args, direction)
	}

	if search != "" {
		searchPattern := "%" + search + "%"
		whereClauses = append(whereClauses, "(sender LIKE ? OR recipient LIKE ? OR body LIKE ?)")
		args = append(args, searchPattern, searchPattern, searchPattern)
	}

	whereSQL := ""
	if len(whereClauses) > 0 {
		whereSQL = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	// Count total
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM messages %s", whereSQL)
	var total int
	if err := s.db.QueryRow(countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	// Select rows
	listQuery := fmt.Sprintf(`SELECT id, direction, device_id, sim_card_id, sim_slot, sender, recipient, body, status, idempotency_key, parts_count, error_message, scheduled_at, sent_at, delivered_at, created_at FROM messages %s ORDER BY created_at DESC LIMIT ? OFFSET ?`, whereSQL)
	queryArgs := append(args, limit, offset)

	rows, err := s.db.Query(listQuery, queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var messages []domain.Message
	for rows.Next() {
		var m domain.Message
		if err := rows.Scan(&m.ID, &m.Direction, &m.DeviceID, &m.SimCardID, &m.SimSlot, &m.Sender, &m.Recipient, &m.Body, &m.Status, &m.IdempotencyKey, &m.PartsCount, &m.ErrorMessage, &m.ScheduledAt, &m.SentAt, &m.DeliveredAt, &m.CreatedAt); err != nil {
			return nil, 0, err
		}
		messages = append(messages, m)
	}
	return messages, total, nil
}

// Webhook operations
func (s *DB) CreateWebhook(w *domain.Webhook) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	eventsJSON, _ := json.Marshal(w.Events)
	isActive := 0
	if w.IsActive {
		isActive = 1
	}

	query := `INSERT INTO webhooks (id, url, secret, events, is_active, created_at) VALUES (?, ?, ?, ?, ?, ?)`
	_, err := s.db.Exec(query, w.ID, w.URL, w.Secret, string(eventsJSON), isActive, w.CreatedAt)
	return err
}

func (s *DB) ListActiveWebhooksForEvent(eventType string) ([]domain.Webhook, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT id, url, secret, events, is_active, created_at FROM webhooks WHERE is_active = 1`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var webhooks []domain.Webhook
	for rows.Next() {
		var w domain.Webhook
		var eventsJSON string
		var isActive int
		if err := rows.Scan(&w.ID, &w.URL, &w.Secret, &eventsJSON, &isActive, &w.CreatedAt); err != nil {
			return nil, err
		}
		w.IsActive = isActive == 1
		_ = json.Unmarshal([]byte(eventsJSON), &w.Events)

		// Check if event is subscribed
		for _, e := range w.Events {
			if e == eventType || e == "*" {
				webhooks = append(webhooks, w)
				break
			}
		}
	}
	return webhooks, nil
}

func (s *DB) ListWebhooks() ([]domain.Webhook, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT id, url, secret, events, is_active, created_at FROM webhooks ORDER BY created_at DESC`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var webhooks []domain.Webhook
	for rows.Next() {
		var w domain.Webhook
		var eventsJSON string
		var isActive int
		if err := rows.Scan(&w.ID, &w.URL, &w.Secret, &eventsJSON, &isActive, &w.CreatedAt); err != nil {
			return nil, err
		}
		w.IsActive = isActive == 1
		_ = json.Unmarshal([]byte(eventsJSON), &w.Events)
		webhooks = append(webhooks, w)
	}
	return webhooks, nil
}

func (s *DB) DeleteWebhook(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("DELETE FROM webhooks WHERE id = ?", id)
	return err
}

// Webhook Delivery Log operations
func (s *DB) SaveWebhookDelivery(d *domain.WebhookDelivery) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `
	INSERT INTO webhook_deliveries (id, webhook_id, message_id, event_type, payload, response_status, response_body, latency_ms, attempt, status, next_retry_at, created_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		response_status = excluded.response_status,
		response_body = excluded.response_body,
		latency_ms = excluded.latency_ms,
		attempt = excluded.attempt,
		status = excluded.status,
		next_retry_at = excluded.next_retry_at;
	`
	_, err := s.db.Exec(query, d.ID, d.WebhookID, d.MessageID, d.EventType, d.Payload, d.ResponseStatus, d.ResponseBody, d.LatencyMs, d.Attempt, d.Status, d.NextRetryAt, d.CreatedAt)
	return err
}

func (s *DB) ListWebhookDeliveries(webhookID string, limit int) ([]domain.WebhookDelivery, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT id, webhook_id, message_id, event_type, payload, response_status, response_body, latency_ms, attempt, status, next_retry_at, created_at FROM webhook_deliveries `
	var args []interface{}
	if webhookID != "" {
		query += `WHERE webhook_id = ? `
		args = append(args, webhookID)
	}
	query += `ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deliveries []domain.WebhookDelivery
	for rows.Next() {
		var d domain.WebhookDelivery
		if err := rows.Scan(&d.ID, &d.WebhookID, &d.MessageID, &d.EventType, &d.Payload, &d.ResponseStatus, &d.ResponseBody, &d.LatencyMs, &d.Attempt, &d.Status, &d.NextRetryAt, &d.CreatedAt); err != nil {
			return nil, err
		}
		deliveries = append(deliveries, d)
	}
	return deliveries, nil
}

// ApiToken operations
func (s *DB) CreateApiToken(t *domain.ApiToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	scopesJSON, _ := json.Marshal(t.Scopes)
	query := `INSERT INTO api_tokens (id, name, token_prefix, token_hash, scopes, last_used_at, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := s.db.Exec(query, t.ID, t.Name, t.TokenPrefix, t.TokenHash, string(scopesJSON), t.LastUsedAt, t.ExpiresAt, t.CreatedAt)
	return err
}

func (s *DB) GetApiTokenByHash(tokenHash string) (*domain.ApiToken, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT id, name, token_prefix, token_hash, scopes, last_used_at, expires_at, created_at FROM api_tokens WHERE token_hash = ?`
	row := s.db.QueryRow(query, tokenHash)

	var t domain.ApiToken
	var scopesJSON string
	err := row.Scan(&t.ID, &t.Name, &t.TokenPrefix, &t.TokenHash, &scopesJSON, &t.LastUsedAt, &t.ExpiresAt, &t.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(scopesJSON), &t.Scopes)
	return &t, nil
}

func (s *DB) TouchApiToken(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("UPDATE api_tokens SET last_used_at = ? WHERE id = ?", time.Now().UTC(), id)
	return err
}

func (s *DB) ListApiTokens() ([]domain.ApiToken, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT id, name, token_prefix, token_hash, scopes, last_used_at, expires_at, created_at FROM api_tokens ORDER BY created_at DESC`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tokens []domain.ApiToken
	for rows.Next() {
		var t domain.ApiToken
		var scopesJSON string
		if err := rows.Scan(&t.ID, &t.Name, &t.TokenPrefix, &t.TokenHash, &scopesJSON, &t.LastUsedAt, &t.ExpiresAt, &t.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(scopesJSON), &t.Scopes)
		tokens = append(tokens, t)
	}
	return tokens, nil
}

func (s *DB) DeleteApiToken(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("DELETE FROM api_tokens WHERE id = ?", id)
	return err
}

// OTA Release operations
func (s *DB) SaveOtaRelease(r *domain.OtaRelease) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	isActive := 0
	if r.IsActive {
		isActive = 1
	}

	query := `
	INSERT INTO ota_releases (id, target, version, version_code, file_name, file_path, checksum_sha256, file_size, changelog, is_active, created_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := s.db.Exec(query, r.ID, r.Target, r.Version, r.VersionCode, r.FileName, r.FilePath, r.ChecksumSHA256, r.FileSize, r.Changelog, isActive, r.CreatedAt)
	return err
}

func (s *DB) GetLatestOtaRelease(target string) (*domain.OtaRelease, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT id, target, version, version_code, file_name, file_path, checksum_sha256, file_size, changelog, is_active, created_at FROM ota_releases WHERE target = ? AND is_active = 1 ORDER BY version_code DESC LIMIT 1`
	row := s.db.QueryRow(query, target)

	var r domain.OtaRelease
	var isActive int
	err := row.Scan(&r.ID, &r.Target, &r.Version, &r.VersionCode, &r.FileName, &r.FilePath, &r.ChecksumSHA256, &r.FileSize, &r.Changelog, &isActive, &r.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.IsActive = isActive == 1
	return &r, nil
}

func (s *DB) ListOtaReleases() ([]domain.OtaRelease, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT id, target, version, version_code, file_name, file_path, checksum_sha256, file_size, changelog, is_active, created_at FROM ota_releases ORDER BY created_at DESC`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var releases []domain.OtaRelease
	for rows.Next() {
		var r domain.OtaRelease
		var isActive int
		if err := rows.Scan(&r.ID, &r.Target, &r.Version, &r.VersionCode, &r.FileName, &r.FilePath, &r.ChecksumSHA256, &r.FileSize, &r.Changelog, &isActive, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.IsActive = isActive == 1
		releases = append(releases, r)
	}
	return releases, nil
}

