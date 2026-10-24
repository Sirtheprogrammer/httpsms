package handler

import (
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/httpsms/gateway-server/internal/api/middleware"
	"github.com/httpsms/gateway-server/internal/domain"
	"github.com/httpsms/gateway-server/internal/storage"
	"golang.org/x/crypto/bcrypt"
)

const (
	minPasswordLen = 8
	bcryptCost     = 12
)

// dummyHash keeps login timing the same whether or not the email exists.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("timing-equalizer"), bcryptCost)

type userView struct {
	*domain.User
	IsAdmin bool `json:"is_admin"`
}

func viewOf(u *domain.User) userView { return userView{User: u, IsAdmin: u.IsAdmin()} }

func isSecureRequest(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (h *APIHandler) startSession(w http.ResponseWriter, r *http.Request, u *domain.User) error {
	raw := middleware.NewRandomToken("", 32)
	expires := time.Now().UTC().Add(middleware.SessionLifetime)
	if err := h.store.CreateSession(middleware.HashToken(raw), u.ID, expires); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     middleware.SessionCookie,
		Value:    raw,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   isSecureRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
	now := time.Now().UTC()
	u.LastLoginAt = &now
	_ = h.store.UpdateUser(u)
	return nil
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: middleware.SessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: isSecureRequest(r), SameSite: http.SameSiteLaxMode,
	})
}

func normalizeEmail(raw string) (string, bool) {
	email := strings.ToLower(strings.TrimSpace(raw))
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || len(email) > 254 {
		return "", false
	}
	return email, true
}

// AuthStatus handles GET /api/v1/auth/status (public): lets the sign-in page say
// "create the admin account" on a fresh install.
func (h *APIHandler) AuthStatus(w http.ResponseWriter, r *http.Request) {
	n, err := h.store.CountUsers()
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"has_users":         n > 0,
		"registration_open": true,
	})
}

// Register handles POST /api/v1/auth/register (public, open sign-up).
// The first account becomes the admin and inherits any pre-existing data.
func (h *APIHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	email, ok := normalizeEmail(req.Email)
	if !ok {
		WriteError(w, http.StatusBadRequest, "VALIDATION_FAILED", "Enter a valid email address")
		return
	}
	if len(req.Password) < minPasswordLen || len(req.Password) > 72 {
		WriteError(w, http.StatusBadRequest, "VALIDATION_FAILED", "Password must be 8-72 characters")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Could not secure password")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = strings.Split(email, "@")[0]
	}

	user := &domain.User{
		ID:           uuid.New().String(),
		Email:        email,
		Name:         name,
		Role:         domain.RoleUser,
		PasswordHash: string(hash),
		CreatedAt:    time.Now().UTC(),
	}
	if err := h.store.CreateUser(user); err != nil {
		if err == storage.ErrEmailTaken {
			WriteError(w, http.StatusConflict, "EMAIL_TAKEN", err.Error())
			return
		}
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	if err := h.startSession(w, r, user); err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Account created but sign-in failed")
		return
	}
	WriteJSON(w, http.StatusCreated, viewOf(user))
}

// Login handles POST /api/v1/auth/login (public, rate limited).
func (h *APIHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	email, _ := normalizeEmail(req.Email)
	user, err := h.store.GetUserByEmail(email)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	hash := dummyHash
	if user != nil {
		hash = []byte(user.PasswordHash)
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(req.Password)) != nil || user == nil {
		WriteError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "Email or password is incorrect")
		return
	}
	if user.Disabled {
		WriteError(w, http.StatusForbidden, "ACCOUNT_DISABLED", "This account has been disabled by an administrator")
		return
	}
	if err := h.startSession(w, r, user); err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Could not start session")
		return
	}
	WriteJSON(w, http.StatusOK, viewOf(user))
}

// Logout handles POST /api/v1/auth/logout.
func (h *APIHandler) Logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(middleware.SessionCookie); err == nil {
		_ = h.store.DeleteSession(middleware.HashToken(c.Value))
	}
	clearSessionCookie(w, r)
	WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// Me handles GET /api/v1/auth/me.
func (h *APIHandler) Me(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if p.User == nil {
		WriteJSON(w, http.StatusOK, map[string]interface{}{"id": "", "name": "Master secret", "role": domain.RoleAdmin, "is_admin": true})
		return
	}
	v := viewOf(p.User)
	v.IsAdmin = p.IsAdmin()
	WriteJSON(w, http.StatusOK, v)
}

// UpdateMe handles PATCH /api/v1/auth/me (display name).
func (h *APIHandler) UpdateMe(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if p.User == nil {
		WriteError(w, http.StatusBadRequest, "NO_ACCOUNT", "Not signed in as a user")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if name := strings.TrimSpace(req.Name); name != "" {
		p.User.Name = name
	}
	if err := h.store.UpdateUser(p.User); err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, viewOf(p.User))
}

// ChangePassword handles POST /api/v1/auth/password and signs out other sessions.
func (h *APIHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if p.User == nil {
		WriteError(w, http.StatusBadRequest, "NO_ACCOUNT", "Not signed in as a user")
		return
	}
	var req struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(p.User.PasswordHash), []byte(req.Current)) != nil {
		WriteError(w, http.StatusBadRequest, "INVALID_CREDENTIALS", "Current password is incorrect")
		return
	}
	if len(req.New) < minPasswordLen || len(req.New) > 72 {
		WriteError(w, http.StatusBadRequest, "VALIDATION_FAILED", "Password must be 8-72 characters")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.New), bcryptCost)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	p.User.PasswordHash = string(hash)
	if err := h.store.UpdateUser(p.User); err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	keep := ""
	if c, err := r.Cookie(middleware.SessionCookie); err == nil {
		keep = middleware.HashToken(c.Value)
	}
	_ = h.store.DeleteUserSessions(p.User.ID, keep)
	WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// --- Admin: user management ---

func (h *APIHandler) AdminListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.store.ListUsers()
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, users)
}

// wouldRemoveLastAdmin blocks demoting, disabling or deleting the only active admin.
func (h *APIHandler) wouldRemoveLastAdmin(target *domain.User) bool {
	if !target.IsAdmin() || target.Disabled {
		return false
	}
	n, err := h.store.CountActiveAdmins()
	return err != nil || n <= 1
}

func (h *APIHandler) AdminUpdateUser(w http.ResponseWriter, r *http.Request) {
	target, err := h.store.GetUser(chi.URLParam(r, "id"))
	if err != nil || target == nil {
		notFound(w, "User")
		return
	}
	var req struct {
		Role     *string `json:"role"`
		Disabled *bool   `json:"disabled"`
		Name     *string `json:"name"`
		Password *string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	losingAdmin := (req.Role != nil && *req.Role != domain.RoleAdmin) || (req.Disabled != nil && *req.Disabled)
	if losingAdmin && h.wouldRemoveLastAdmin(target) {
		WriteError(w, http.StatusConflict, "LAST_ADMIN", "Promote another user to admin first")
		return
	}
	if req.Role != nil {
		if *req.Role != domain.RoleAdmin && *req.Role != domain.RoleUser {
			WriteError(w, http.StatusBadRequest, "VALIDATION_FAILED", "Role must be admin or user")
			return
		}
		target.Role = *req.Role
	}
	if req.Disabled != nil {
		target.Disabled = *req.Disabled
	}
	if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
		target.Name = strings.TrimSpace(*req.Name)
	}
	if req.Password != nil {
		if len(*req.Password) < minPasswordLen || len(*req.Password) > 72 {
			WriteError(w, http.StatusBadRequest, "VALIDATION_FAILED", "Password must be 8-72 characters")
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(*req.Password), bcryptCost)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
			return
		}
		target.PasswordHash = string(hash)
	}
	if err := h.store.UpdateUser(target); err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	if target.Disabled || req.Password != nil {
		_ = h.store.DeleteUserSessions(target.ID, "")
	}
	WriteJSON(w, http.StatusOK, viewOf(target))
}

func (h *APIHandler) AdminDeleteUser(w http.ResponseWriter, r *http.Request) {
	target, err := h.store.GetUser(chi.URLParam(r, "id"))
	if err != nil || target == nil {
		notFound(w, "User")
		return
	}
	if p := principal(r); p.User != nil && p.User.ID == target.ID {
		WriteError(w, http.StatusConflict, "SELF_DELETE", "You can't delete your own account here")
		return
	}
	if h.wouldRemoveLastAdmin(target) {
		WriteError(w, http.StatusConflict, "LAST_ADMIN", "Promote another user to admin first")
		return
	}
	devices, _ := h.store.ListDevices(target.ID)
	if err := h.store.DeleteUser(target.ID); err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	for _, d := range devices {
		h.hub.Disconnect(d.ID)
	}
	WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
}
