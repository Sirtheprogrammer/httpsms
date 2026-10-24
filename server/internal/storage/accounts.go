package storage

import (
	"database/sql"
	"errors"
	"time"

	"github.com/httpsms/gateway-server/internal/domain"
)

var ErrEmailTaken = errors.New("an account with this email already exists")

// User operations

const userColumns = `id, email, name, role, password_hash, disabled, last_login_at, created_at`

func scanUser(sc interface{ Scan(...interface{}) error }) (*domain.User, error) {
	var u domain.User
	var disabled int
	if err := sc.Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.PasswordHash, &disabled, &u.LastLoginAt, &u.CreatedAt); err != nil {
		return nil, err
	}
	u.Disabled = disabled == 1
	return &u, nil
}

func (s *DB) CountUsers() (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// CreateUser inserts a user. When it is the very first account it is promoted to
// admin and adopts every row created before multi-user support existed.
func (s *DB) CreateUser(u *domain.User) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM users WHERE email = ?`, u.Email).Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		return ErrEmailTaken
	}

	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return err
	}
	first := count == 0
	if first {
		u.Role = domain.RoleAdmin
	}

	if _, err := tx.Exec(`INSERT INTO users (`+userColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.Email, u.Name, u.Role, u.PasswordHash, boolInt(u.Disabled), u.LastLoginAt, u.CreatedAt); err != nil {
		return err
	}

	if first {
		for _, table := range []string{"devices", "messages", "webhooks", "api_tokens"} {
			if _, err := tx.Exec(`UPDATE `+table+` SET user_id = ? WHERE user_id = ''`, u.ID); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *DB) getUserWhere(where string, arg interface{}) (*domain.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, err := scanUser(s.db.QueryRow(`SELECT `+userColumns+` FROM users WHERE `+where, arg))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func (s *DB) GetUser(id string) (*domain.User, error) { return s.getUserWhere("id = ?", id) }

func (s *DB) GetUserByEmail(email string) (*domain.User, error) {
	return s.getUserWhere("email = ?", email)
}

// FirstAdmin is the account that owns data created with the MASTER_SECRET.
func (s *DB) FirstAdmin() (*domain.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, err := scanUser(s.db.QueryRow(`SELECT ` + userColumns + ` FROM users WHERE role = 'admin' AND disabled = 0 ORDER BY created_at ASC LIMIT 1`))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

// UserSummary adds per-user usage counts for the admin user list.
type UserSummary struct {
	domain.User
	Devices   int `json:"devices"`
	Messages  int `json:"messages"`
	Workflows int `json:"workflows"`
}

func (s *DB) ListUsers() ([]UserSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT ` + userColumns + `,
		(SELECT COUNT(*) FROM devices d WHERE d.user_id = users.id),
		(SELECT COUNT(*) FROM messages m WHERE m.user_id = users.id),
		(SELECT COUNT(*) FROM workflows w WHERE w.user_id = users.id)
		FROM users ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []UserSummary{}
	for rows.Next() {
		var us UserSummary
		var disabled int
		if err := rows.Scan(&us.ID, &us.Email, &us.Name, &us.Role, &us.PasswordHash, &disabled, &us.LastLoginAt, &us.CreatedAt,
			&us.Devices, &us.Messages, &us.Workflows); err != nil {
			return nil, err
		}
		us.Disabled = disabled == 1
		users = append(users, us)
	}
	return users, nil
}

func (s *DB) CountActiveAdmins() (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users WHERE role = 'admin' AND disabled = 0`).Scan(&n)
	return n, err
}

func (s *DB) UpdateUser(u *domain.User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE users SET name = ?, role = ?, password_hash = ?, disabled = ?, last_login_at = ? WHERE id = ?`,
		u.Name, u.Role, u.PasswordHash, boolInt(u.Disabled), u.LastLoginAt, u.ID)
	return err
}

// DeleteUser removes an account and everything it owns.
func (s *DB) DeleteUser(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range []string{"workflow_runs", "workflows", "ai_credentials", "messages", "webhooks", "api_tokens", "devices", "sessions"} {
		if _, err := tx.Exec(`DELETE FROM `+table+` WHERE user_id = ?`, id); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM users WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// Session operations (dashboard logins)

func (s *DB) CreateSession(tokenHash, userID string, expiresAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO sessions (token_hash, user_id, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		tokenHash, userID, expiresAt, time.Now().UTC())
	return err
}

// GetSessionUser returns the user for a live session, or nil if expired/unknown/disabled.
func (s *DB) GetSessionUser(tokenHash string) (*domain.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, err := scanUser(s.db.QueryRow(`SELECT u.id, u.email, u.name, u.role, u.password_hash, u.disabled, u.last_login_at, u.created_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND s.expires_at > ? AND u.disabled = 0`, tokenHash, time.Now().UTC()))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func (s *DB) DeleteSession(tokenHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

func (s *DB) DeleteUserSessions(userID, exceptHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM sessions WHERE user_id = ? AND token_hash != ?`, userID, exceptHash)
	return err
}

func (s *DB) PruneExpiredSessions() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, time.Now().UTC())
	return err
}

// AI credential operations (BYOK keys, encrypted by the caller)

const credentialColumns = `id, user_id, provider, label, base_url, key_hint, encrypted_key, created_at`

func scanCredential(sc interface{ Scan(...interface{}) error }) (*domain.AICredential, error) {
	var c domain.AICredential
	if err := sc.Scan(&c.ID, &c.UserID, &c.Provider, &c.Label, &c.BaseURL, &c.KeyHint, &c.EncryptedKey, &c.CreatedAt); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *DB) CreateAICredential(c *domain.AICredential) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO ai_credentials (`+credentialColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.UserID, c.Provider, c.Label, c.BaseURL, c.KeyHint, c.EncryptedKey, c.CreatedAt)
	return err
}

// ListAICredentials always filters by user: keys are private even from admins.
func (s *DB) ListAICredentials(userID string) ([]domain.AICredential, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT `+credentialColumns+` FROM ai_credentials WHERE user_id = ? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	creds := []domain.AICredential{}
	for rows.Next() {
		c, err := scanCredential(rows)
		if err != nil {
			return nil, err
		}
		creds = append(creds, *c)
	}
	return creds, nil
}

func (s *DB) GetAICredential(id string) (*domain.AICredential, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, err := scanCredential(s.db.QueryRow(`SELECT `+credentialColumns+` FROM ai_credentials WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return c, err
}

func (s *DB) DeleteAICredential(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM ai_credentials WHERE id = ?`, id)
	return err
}

// Workflow operations

const workflowColumns = `id, user_id, name, description, enabled, graph, created_at, updated_at`

func scanWorkflow(sc interface{ Scan(...interface{}) error }) (*domain.Workflow, error) {
	var w domain.Workflow
	var enabled int
	var graph string
	if err := sc.Scan(&w.ID, &w.UserID, &w.Name, &w.Description, &enabled, &graph, &w.CreatedAt, &w.UpdatedAt); err != nil {
		return nil, err
	}
	w.Enabled = enabled == 1
	w.Graph = []byte(graph)
	return &w, nil
}

func (s *DB) SaveWorkflow(w *domain.Workflow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO workflows (`+workflowColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name = excluded.name, description = excluded.description,
		enabled = excluded.enabled, graph = excluded.graph, updated_at = excluded.updated_at`,
		w.ID, w.UserID, w.Name, w.Description, boolInt(w.Enabled), string(w.Graph), w.CreatedAt, w.UpdatedAt)
	return err
}

func (s *DB) GetWorkflow(id string) (*domain.Workflow, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	w, err := scanWorkflow(s.db.QueryRow(`SELECT `+workflowColumns+` FROM workflows WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return w, err
}

func (s *DB) ListWorkflows(owner string, enabledOnly bool) ([]domain.Workflow, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	query := `SELECT ` + workflowColumns + ` FROM workflows WHERE 1=1`
	var args []interface{}
	if owner != "" {
		query += ` AND user_id = ?`
		args = append(args, owner)
	}
	if enabledOnly {
		query += ` AND enabled = 1`
	}
	rows, err := s.db.Query(query+` ORDER BY updated_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	workflows := []domain.Workflow{}
	for rows.Next() {
		w, err := scanWorkflow(rows)
		if err != nil {
			return nil, err
		}
		workflows = append(workflows, *w)
	}
	return workflows, nil
}

func (s *DB) DeleteWorkflow(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.Exec(`DELETE FROM workflow_runs WHERE workflow_id = ?`, id); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM workflows WHERE id = ?`, id)
	return err
}

const runColumns = `id, workflow_id, user_id, trigger_message_id, status, dry_run, steps, error, started_at, finished_at`

func (s *DB) SaveWorkflowRun(r *domain.WorkflowRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	steps := string(r.Steps)
	if steps == "" {
		steps = "[]"
	}
	_, err := s.db.Exec(`INSERT INTO workflow_runs (`+runColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET status = excluded.status, steps = excluded.steps,
		error = excluded.error, finished_at = excluded.finished_at`,
		r.ID, r.WorkflowID, r.UserID, r.TriggerMessageID, r.Status, boolInt(r.DryRun), steps, r.Error, r.StartedAt, r.FinishedAt)
	return err
}

func (s *DB) ListWorkflowRuns(owner, workflowID string, limit int) ([]domain.WorkflowRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	query := `SELECT ` + runColumns + ` FROM workflow_runs WHERE 1=1`
	var args []interface{}
	if owner != "" {
		query += ` AND user_id = ?`
		args = append(args, owner)
	}
	if workflowID != "" {
		query += ` AND workflow_id = ?`
		args = append(args, workflowID)
	}
	query += ` ORDER BY started_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []domain.WorkflowRun{}
	for rows.Next() {
		var r domain.WorkflowRun
		var dry int
		var steps string
		if err := rows.Scan(&r.ID, &r.WorkflowID, &r.UserID, &r.TriggerMessageID, &r.Status, &dry, &steps, &r.Error, &r.StartedAt, &r.FinishedAt); err != nil {
			return nil, err
		}
		r.DryRun = dry == 1
		r.Steps = []byte(steps)
		runs = append(runs, r)
	}
	return runs, nil
}

// CountRecentRuns counts non-dry runs of a workflow triggered by messages from
// sender since t; used to stop two auto-replying gateways looping forever.
func (s *DB) CountRecentRuns(workflowID, sender string, since time.Time) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM workflow_runs r JOIN messages m ON m.id = r.trigger_message_id
		WHERE r.workflow_id = ? AND m.sender = ? AND r.started_at >= ? AND r.dry_run = 0 AND r.status != 'skipped'`,
		workflowID, sender, since).Scan(&n)
	return n, err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
