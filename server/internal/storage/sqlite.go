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

// DB wraps SQLite. Methods that take an `owner` argument scope results to that
// user's rows; an empty owner means "all users" and is only passed for admins.
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

	CREATE TABLE IF NOT EXISTS users (
		id TEXT PRIMARY KEY,
		email TEXT UNIQUE NOT NULL COLLATE NOCASE,
		name TEXT NOT NULL DEFAULT '',
		role TEXT NOT NULL DEFAULT 'user',
		password_hash TEXT NOT NULL,
		disabled INTEGER NOT NULL DEFAULT 0,
		last_login_at DATETIME,
		created_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS sessions (
		token_hash TEXT PRIMARY KEY,
		user_id TEXT NOT NULL,
		expires_at DATETIME NOT NULL,
		created_at DATETIME NOT NULL,
		FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS ai_credentials (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL,
		provider TEXT NOT NULL,
		label TEXT NOT NULL DEFAULT '',
		base_url TEXT NOT NULL DEFAULT '',
		key_hint TEXT NOT NULL DEFAULT '',
		encrypted_key BLOB NOT NULL,
		created_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS workflows (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL,
		name TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		enabled INTEGER NOT NULL DEFAULT 0,
		graph TEXT NOT NULL,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS workflow_runs (
		id TEXT PRIMARY KEY,
		workflow_id TEXT NOT NULL,
		user_id TEXT NOT NULL,
		trigger_message_id TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL,
		dry_run INTEGER NOT NULL DEFAULT 0,
		steps TEXT NOT NULL DEFAULT '[]',
		error TEXT NOT NULL DEFAULT '',
		started_at DATETIME NOT NULL,
		finished_at DATETIME,
		FOREIGN KEY(workflow_id) REFERENCES workflows(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_messages_idempotency ON messages(idempotency_key);
	CREATE INDEX IF NOT EXISTS idx_messages_created_at ON messages(created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_messages_direction ON messages(direction);
	CREATE INDEX IF NOT EXISTS idx_messages_status ON messages(status);
	CREATE INDEX IF NOT EXISTS idx_sim_cards_device ON sim_cards(device_id);
	CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_webhook ON webhook_deliveries(webhook_id);
	CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_status ON webhook_deliveries(status);
	CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
	CREATE INDEX IF NOT EXISTS idx_workflows_user ON workflows(user_id);
	CREATE INDEX IF NOT EXISTS idx_workflow_runs_workflow ON workflow_runs(workflow_id, started_at DESC);
	`
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}

	// Multi-user upgrade: ownership columns on pre-existing tables.
	for _, table := range []string{"devices", "messages", "webhooks", "api_tokens"} {
		if err := s.addColumnIfMissing(table, "user_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	_, err := s.db.Exec(`
	CREATE INDEX IF NOT EXISTS idx_devices_user ON devices(user_id);
	CREATE INDEX IF NOT EXISTS idx_messages_user ON messages(user_id, created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_messages_conversation ON messages(user_id, sender, recipient);
	CREATE INDEX IF NOT EXISTS idx_webhooks_user ON webhooks(user_id);
	CREATE INDEX IF NOT EXISTS idx_api_tokens_user ON api_tokens(user_id);
	`)
	return err
}

func (s *DB) addColumnIfMissing(table, column, definition string) error {
	rows, err := s.db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	rows.Close()
	_, err = s.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, definition))
	return err
}

// ownerClause returns a SQL fragment and args restricting `col` to owner, or nothing for admins.
func ownerClause(col, owner string) (string, []interface{}) {
	if owner == "" {
		return "", nil
	}
	return col + " = ?", []interface{}{owner}
}

// Device operations

const deviceColumns = `id, user_id, name, token_hash, status, battery_level, is_charging, app_version, app_version_code, last_seen_at, created_at`

func scanDevice(sc interface{ Scan(...interface{}) error }) (*domain.Device, error) {
	var d domain.Device
	var isCharging int
	var lastSeen sql.NullTime
	if err := sc.Scan(&d.ID, &d.UserID, &d.Name, &d.TokenHash, &d.Status, &d.BatteryLevel, &isCharging, &d.AppVersion, &d.AppVersionCode, &lastSeen, &d.CreatedAt); err != nil {
		return nil, err
	}
	d.IsCharging = isCharging == 1
	if lastSeen.Valid {
		d.LastSeenAt = lastSeen.Time
	}
	return &d, nil
}

func (s *DB) UpsertDevice(d *domain.Device) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `
	INSERT INTO devices (id, user_id, name, token_hash, status, battery_level, is_charging, app_version, app_version_code, last_seen_at, created_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
	var lastSeen interface{}
	if !d.LastSeenAt.IsZero() {
		lastSeen = d.LastSeenAt
	}

	_, err := s.db.Exec(query, d.ID, d.UserID, d.Name, d.TokenHash, d.Status, d.BatteryLevel, isCharging, d.AppVersion, d.AppVersionCode, lastSeen, d.CreatedAt)
	return err
}

func (s *DB) getDeviceWhere(where string, arg interface{}) (*domain.Device, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	d, err := scanDevice(s.db.QueryRow(`SELECT `+deviceColumns+` FROM devices WHERE `+where, arg))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if sims, err := s.GetDeviceSimCards(d.ID); err == nil {
		d.SimCards = sims
	}
	return d, nil
}

func (s *DB) GetDevice(id string) (*domain.Device, error) {
	return s.getDeviceWhere("id = ?", id)
}

func (s *DB) GetDeviceByTokenHash(tokenHash string) (*domain.Device, error) {
	return s.getDeviceWhere("token_hash = ?", tokenHash)
}

func (s *DB) ListDevices(owner string) ([]domain.Device, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	where, args := ownerClause("user_id", owner)
	query := `SELECT ` + deviceColumns + ` FROM devices`
	if where != "" {
		query += " WHERE " + where
	}
	query += ` ORDER BY last_seen_at DESC, created_at DESC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	devices := []domain.Device{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		devices = append(devices, *d)
	}
	rows.Close()
	for i := range devices {
		if sims, err := s.GetDeviceSimCards(devices[i].ID); err == nil {
			devices[i].SimCards = sims
		}
	}
	return devices, nil
}

// MarkAllDevicesOffline resets stale "online" rows left behind by a previous
// server process; live connections flip them back on their next heartbeat.
func (s *DB) MarkAllDevicesOffline() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("UPDATE devices SET status = 'offline'")
	return err
}

func (s *DB) RenameDevice(id, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("UPDATE devices SET name = ? WHERE id = ?", name, id)
	return err
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
		// Phones don't assign SIM IDs, and an empty ID collides on the primary key
		// as soon as a second SIM is present.
		if sim.ID == "" {
			sim.ID = fmt.Sprintf("%s-sim%d", deviceID, sim.SlotIndex)
		}
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

	sims := []domain.SimCard{}
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

const messageColumns = `id, user_id, direction, device_id, sim_card_id, sim_slot, sender, recipient, body, status, idempotency_key, parts_count, error_message, scheduled_at, sent_at, delivered_at, created_at`

func scanMessage(sc interface{ Scan(...interface{}) error }) (*domain.Message, error) {
	var m domain.Message
	err := sc.Scan(&m.ID, &m.UserID, &m.Direction, &m.DeviceID, &m.SimCardID, &m.SimSlot, &m.Sender, &m.Recipient, &m.Body, &m.Status, &m.IdempotencyKey, &m.PartsCount, &m.ErrorMessage, &m.ScheduledAt, &m.SentAt, &m.DeliveredAt, &m.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *DB) SaveMessage(m *domain.Message) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check idempotency key for deduplication
	if m.IdempotencyKey != "" {
		var existingID string
		err := s.db.QueryRow("SELECT id FROM messages WHERE idempotency_key = ?", m.IdempotencyKey).Scan(&existingID)
		if err == nil {
			m.ID = existingID
			return false, nil // false indicates already existed / was duplicate
		}
	}

	query := `INSERT INTO messages (` + messageColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := s.db.Exec(query, m.ID, m.UserID, m.Direction, m.DeviceID, m.SimCardID, m.SimSlot, m.Sender, m.Recipient, m.Body, m.Status, m.IdempotencyKey, m.PartsCount, m.ErrorMessage, m.ScheduledAt, m.SentAt, m.DeliveredAt, m.CreatedAt)
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *DB) SetMessageDevice(id, deviceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE messages SET device_id = ? WHERE id = ?`, deviceID, id)
	return err
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

	m, err := scanMessage(s.db.QueryRow(`SELECT `+messageColumns+` FROM messages WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return m, err
}

// MessageFilter narrows ListMessages; zero values mean "any".
type MessageFilter struct {
	Owner     string
	Direction string
	Search    string
	Contact   string // messages exchanged with this phone number
	Limit     int
	Offset    int
}

func (s *DB) ListMessages(f MessageFilter) ([]domain.Message, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var whereClauses []string
	var args []interface{}

	if clause, a := ownerClause("user_id", f.Owner); clause != "" {
		whereClauses = append(whereClauses, clause)
		args = append(args, a...)
	}
	if f.Direction != "" {
		whereClauses = append(whereClauses, "direction = ?")
		args = append(args, f.Direction)
	}
	if f.Contact != "" {
		whereClauses = append(whereClauses, "(sender = ? OR recipient = ?)")
		args = append(args, f.Contact, f.Contact)
	}
	if f.Search != "" {
		searchPattern := "%" + f.Search + "%"
		whereClauses = append(whereClauses, "(sender LIKE ? OR recipient LIKE ? OR body LIKE ? OR id = ?)")
		args = append(args, searchPattern, searchPattern, searchPattern, f.Search)
	}

	whereSQL := ""
	if len(whereClauses) > 0 {
		whereSQL = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	var total int
	if err := s.db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM messages %s", whereSQL), args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	if f.Limit <= 0 {
		f.Limit = 30
	}
	listQuery := fmt.Sprintf(`SELECT %s FROM messages %s ORDER BY created_at DESC LIMIT ? OFFSET ?`, messageColumns, whereSQL)
	rows, err := s.db.Query(listQuery, append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	messages := []domain.Message{}
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, 0, err
		}
		messages = append(messages, *m)
	}
	return messages, total, nil
}

// MessageStats are the dashboard overview counters.
type MessageStats struct {
	Inbound      int `json:"total_inbound"`
	Outbound     int `json:"total_outbound"`
	Failed       int `json:"total_failed"`
	InboundToday int `json:"inbound_24h"`
	SentToday    int `json:"outbound_24h"`
}

func (s *DB) GetMessageStats(owner string) (MessageStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	where, args := ownerClause("user_id", owner)
	if where == "" {
		where = "1=1"
	}
	since := time.Now().UTC().Add(-24 * time.Hour)
	var st MessageStats
	err := s.db.QueryRow(`SELECT
		COALESCE(SUM(direction = 'inbound'), 0),
		COALESCE(SUM(direction = 'outbound'), 0),
		COALESCE(SUM(status = 'failed'), 0),
		COALESCE(SUM(direction = 'inbound' AND created_at >= ?), 0),
		COALESCE(SUM(direction = 'outbound' AND created_at >= ?), 0)
		FROM messages WHERE `+where, append([]interface{}{since, since}, args...)...).
		Scan(&st.Inbound, &st.Outbound, &st.Failed, &st.InboundToday, &st.SentToday)
	return st, err
}

// Webhook operations

func scanWebhook(sc interface{ Scan(...interface{}) error }) (*domain.Webhook, error) {
	var w domain.Webhook
	var eventsJSON string
	var isActive int
	if err := sc.Scan(&w.ID, &w.UserID, &w.URL, &w.Secret, &eventsJSON, &isActive, &w.CreatedAt); err != nil {
		return nil, err
	}
	w.IsActive = isActive == 1
	_ = json.Unmarshal([]byte(eventsJSON), &w.Events)
	if w.Events == nil {
		w.Events = []string{}
	}
	return &w, nil
}

func (s *DB) CreateWebhook(w *domain.Webhook) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	eventsJSON, _ := json.Marshal(w.Events)
	isActive := 0
	if w.IsActive {
		isActive = 1
	}

	query := `INSERT INTO webhooks (id, user_id, url, secret, events, is_active, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`
	_, err := s.db.Exec(query, w.ID, w.UserID, w.URL, w.Secret, string(eventsJSON), isActive, w.CreatedAt)
	return err
}

// ListActiveWebhooksForEvent returns the owner's active webhooks subscribed to eventType.
func (s *DB) ListActiveWebhooksForEvent(owner, eventType string) ([]domain.Webhook, error) {
	all, err := s.ListWebhooks(owner)
	if err != nil {
		return nil, err
	}
	webhooks := []domain.Webhook{}
	for _, w := range all {
		if !w.IsActive {
			continue
		}
		for _, e := range w.Events {
			if e == eventType || e == "*" {
				webhooks = append(webhooks, w)
				break
			}
		}
	}
	return webhooks, nil
}

func (s *DB) ListWebhooks(owner string) ([]domain.Webhook, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	where, args := ownerClause("user_id", owner)
	query := `SELECT id, user_id, url, secret, events, is_active, created_at FROM webhooks`
	if where != "" {
		query += " WHERE " + where
	}
	rows, err := s.db.Query(query+` ORDER BY created_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	webhooks := []domain.Webhook{}
	for rows.Next() {
		w, err := scanWebhook(rows)
		if err != nil {
			return nil, err
		}
		webhooks = append(webhooks, *w)
	}
	return webhooks, nil
}

func (s *DB) GetWebhook(id string) (*domain.Webhook, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	w, err := scanWebhook(s.db.QueryRow(`SELECT id, user_id, url, secret, events, is_active, created_at FROM webhooks WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return w, err
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

// ListWebhookDeliveries returns recent deliveries for the owner's webhooks (all for admins).
func (s *DB) ListWebhookDeliveries(owner, webhookID string, limit int) ([]domain.WebhookDelivery, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT d.id, d.webhook_id, d.message_id, d.event_type, d.payload, d.response_status, d.response_body, d.latency_ms, d.attempt, d.status, d.next_retry_at, d.created_at
		FROM webhook_deliveries d JOIN webhooks w ON w.id = d.webhook_id WHERE 1=1 `
	var args []interface{}
	if owner != "" {
		query += `AND w.user_id = ? `
		args = append(args, owner)
	}
	if webhookID != "" {
		query += `AND d.webhook_id = ? `
		args = append(args, webhookID)
	}
	query += `ORDER BY d.created_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	deliveries := []domain.WebhookDelivery{}
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

const tokenColumns = `id, user_id, name, token_prefix, token_hash, scopes, last_used_at, expires_at, created_at`

func scanToken(sc interface{ Scan(...interface{}) error }) (*domain.ApiToken, error) {
	var t domain.ApiToken
	var scopesJSON string
	if err := sc.Scan(&t.ID, &t.UserID, &t.Name, &t.TokenPrefix, &t.TokenHash, &scopesJSON, &t.LastUsedAt, &t.ExpiresAt, &t.CreatedAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(scopesJSON), &t.Scopes)
	if t.Scopes == nil {
		t.Scopes = []string{}
	}
	return &t, nil
}

func (s *DB) CreateApiToken(t *domain.ApiToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	scopesJSON, _ := json.Marshal(t.Scopes)
	query := `INSERT INTO api_tokens (` + tokenColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := s.db.Exec(query, t.ID, t.UserID, t.Name, t.TokenPrefix, t.TokenHash, string(scopesJSON), t.LastUsedAt, t.ExpiresAt, t.CreatedAt)
	return err
}

func (s *DB) GetApiTokenByHash(tokenHash string) (*domain.ApiToken, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	t, err := scanToken(s.db.QueryRow(`SELECT `+tokenColumns+` FROM api_tokens WHERE token_hash = ?`, tokenHash))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return t, err
}

func (s *DB) GetApiToken(id string) (*domain.ApiToken, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	t, err := scanToken(s.db.QueryRow(`SELECT `+tokenColumns+` FROM api_tokens WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return t, err
}

func (s *DB) TouchApiToken(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("UPDATE api_tokens SET last_used_at = ? WHERE id = ?", time.Now().UTC(), id)
	return err
}

func (s *DB) ListApiTokens(owner string) ([]domain.ApiToken, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	where, args := ownerClause("user_id", owner)
	query := `SELECT ` + tokenColumns + ` FROM api_tokens`
	if where != "" {
		query += " WHERE " + where
	}
	rows, err := s.db.Query(query+` ORDER BY created_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tokens := []domain.ApiToken{}
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, *t)
	}
	return tokens, nil
}

func (s *DB) DeleteApiToken(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("DELETE FROM api_tokens WHERE id = ?", id)
	return err
}

// OTA Release operations (global, admin-managed)
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

	releases := []domain.OtaRelease{}
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
