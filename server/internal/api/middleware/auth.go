package middleware

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/httpsms/gateway-server/internal/domain"
	"github.com/httpsms/gateway-server/internal/storage"
)

type contextKey string

const principalKey contextKey = "principal"

const (
	SessionCookie   = "htsms_session"
	SessionLifetime = 30 * 24 * time.Hour
	// Cookie-authenticated writes must carry this header. Browsers can't add it
	// cross-origin without a CORS preflight, which the server never approves for
	// credentials, so it blocks CSRF.
	CSRFHeader = "X-Requested-With"
)

// Principal is who is making a request.
type Principal struct {
	User      *domain.User // nil only for MASTER_SECRET before any account exists
	Scopes    []string
	ViaCookie bool
	ViaMaster bool
}

// IsAdmin is true for the master secret, an admin's dashboard session, or an
// admin's API token with full ("*") scope; narrower admin tokens act as a normal user.
func (p *Principal) IsAdmin() bool {
	if p.ViaMaster {
		return true
	}
	return p.User.IsAdmin() && (p.ViaCookie || p.HasScope("*"))
}

// UserID is the owner for rows this principal creates.
func (p *Principal) UserID() string {
	if p.User == nil {
		return ""
	}
	return p.User.ID
}

// Owner is the ownership filter for list queries: "" (everything) for admins.
func (p *Principal) Owner() string {
	if p.IsAdmin() {
		return ""
	}
	return p.UserID()
}

// CanAccess reports whether the principal may see/modify a row owned by owner.
func (p *Principal) CanAccess(owner string) bool {
	return p.IsAdmin() || (owner != "" && owner == p.UserID())
}

func (p *Principal) HasScope(scope string) bool {
	for _, s := range p.Scopes {
		if s == "*" || s == scope {
			return true
		}
	}
	return false
}

func FromContext(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey).(*Principal)
	return p
}

// WithPrincipal is used by tests and internal callers.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey, p)
}

type AuthMiddleware struct {
	store        *storage.DB
	masterSecret string
}

func NewAuthMiddleware(store *storage.DB, masterSecret string) *AuthMiddleware {
	return &AuthMiddleware{store: store, masterSecret: masterSecret}
}

func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func NewRandomToken(prefix string, nBytes int) string {
	b := make([]byte, nBytes)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func writeAuthError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":{"code":"` + code + `","message":"` + msg + `"}}`))
}

// Resolve identifies the caller from a session cookie or a Bearer token
// (API token, or the legacy MASTER_SECRET which acts as the first admin).
func (m *AuthMiddleware) Resolve(r *http.Request) (*Principal, error) {
	if c, err := r.Cookie(SessionCookie); err == nil && c.Value != "" {
		user, err := m.store.GetSessionUser(HashToken(c.Value))
		if err != nil {
			return nil, err
		}
		if user != nil {
			return &Principal{User: user, Scopes: []string{"*"}, ViaCookie: true}, nil
		}
	}

	tokenStr := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if tokenStr == "" {
		return nil, nil
	}

	if m.masterSecret != "" && tokenStr == m.masterSecret {
		admin, err := m.store.FirstAdmin()
		if err != nil {
			return nil, err
		}
		return &Principal{User: admin, Scopes: []string{"*"}, ViaMaster: true}, nil
	}

	token, err := m.store.GetApiTokenByHash(HashToken(tokenStr))
	if err != nil || token == nil {
		return nil, err
	}
	if token.ExpiresAt != nil && token.ExpiresAt.Before(time.Now()) {
		return nil, nil
	}
	user, err := m.store.GetUser(token.UserID)
	if err != nil {
		return nil, err
	}
	if user == nil || user.Disabled {
		return nil, nil
	}
	go func(id string) { _ = m.store.TouchApiToken(id) }(token.ID)
	return &Principal{User: user, Scopes: token.Scopes}, nil
}

// Authenticate rejects anonymous requests and stores the Principal in the context.
func (m *AuthMiddleware) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := m.Resolve(r)
		if err != nil {
			writeAuthError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Authentication lookup failed")
			return
		}
		if p == nil {
			writeAuthError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Sign in or provide a valid API token")
			return
		}
		if p.ViaCookie && r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get(CSRFHeader) == "" {
			writeAuthError(w, http.StatusForbidden, "CSRF_CHECK_FAILED", "Missing X-Requested-With header")
			return
		}
		next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
	})
}

// RequireScope must run after Authenticate.
func (m *AuthMiddleware) RequireScope(scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if p := FromContext(r.Context()); p == nil || !p.HasScope(scope) {
				writeAuthError(w, http.StatusForbidden, "FORBIDDEN", "This token lacks the "+scope+" scope")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireAdmin must run after Authenticate.
func (m *AuthMiddleware) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := FromContext(r.Context()); p == nil || !p.IsAdmin() {
			writeAuthError(w, http.StatusForbidden, "FORBIDDEN", "Administrator access required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RateLimiter is a small fixed-window limiter keyed by client IP, used for
// login and registration to slow down password guessing.
type RateLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[string][]time.Time
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{limit: limit, window: window, hits: map[string][]time.Time{}}
}

func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-rl.window)
	recent := rl.hits[key][:0]
	for _, t := range rl.hits[key] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if len(recent) >= rl.limit {
		rl.hits[key] = recent
		return false
	}
	rl.hits[key] = append(recent, now)
	return true
}

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}
		if !rl.Allow(ip) {
			writeAuthError(w, http.StatusTooManyRequests, "RATE_LIMITED", "Too many attempts, try again in a minute")
			return
		}
		next.ServeHTTP(w, r)
	})
}
