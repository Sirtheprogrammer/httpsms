package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/httpsms/gateway-server/internal/domain"
	"github.com/httpsms/gateway-server/internal/storage"
)

type contextKey string

const (
	TokenContextKey  contextKey = "api_token"
	DeviceContextKey contextKey = "device"
)

type AuthMiddleware struct {
	store       *storage.DB
	masterSecret string
}

func NewAuthMiddleware(store *storage.DB, masterSecret string) *AuthMiddleware {
	return &AuthMiddleware{
		store:        store,
		masterSecret: masterSecret,
	}
}

// RequireScope checks if the request has an API token with the specified scope, or master secret
func (m *AuthMiddleware) RequireScope(scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				http.Error(w, `{"error":{"code":"UNAUTHORIZED","message":"Missing Authorization header"}}`, http.StatusUnauthorized)
				return
			}

			tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
			tokenStr = strings.TrimSpace(tokenStr)

			// Check if master secret matches (for dashboard / admin access)
			if m.masterSecret != "" && tokenStr == m.masterSecret {
				// Master has all scopes
				ctx := context.WithValue(r.Context(), TokenContextKey, &domain.ApiToken{
					ID:     "master",
					Name:   "Master Administrator",
					Scopes: []string{"*"},
				})
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// Hash token to look up in DB
			h := sha256.Sum256([]byte(tokenStr))
			tokenHash := hex.EncodeToString(h[:])

			token, err := m.store.GetApiTokenByHash(tokenHash)
			if err != nil || token == nil {
				http.Error(w, `{"error":{"code":"UNAUTHORIZED","message":"Invalid or expired API token"}}`, http.StatusUnauthorized)
				return
			}

			// Check expiration
			if token.ExpiresAt != nil && token.ExpiresAt.Before(time.Now()) {
				http.Error(w, `{"error":{"code":"TOKEN_EXPIRED","message":"API token has expired"}}`, http.StatusUnauthorized)
				return
			}

			// Check scope
			hasScope := false
			for _, s := range token.Scopes {
				if s == scope || s == "*" {
					hasScope = true
					break
				}
			}

			if !hasScope {
				http.Error(w, `{"error":{"code":"FORBIDDEN","message":"Insufficient permissions for this action"}}`, http.StatusForbidden)
				return
			}

			// Update last used asynchronously
			go func(id string) {
				_ = m.store.TouchApiToken(id)
			}(token.ID)

			ctx := context.WithValue(r.Context(), TokenContextKey, token)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// DeviceAuth authenticates Android gateway devices connecting via WebSocket or HTTP
func (m *AuthMiddleware) DeviceAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Device-Token")
		if token == "" {
			token = r.URL.Query().Get("token")
		}
		if token == "" {
			authHeader := r.Header.Get("Authorization")
			token = strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		}

		if token == "" {
			http.Error(w, `{"error":{"code":"UNAUTHORIZED","message":"Device authentication required"}}`, http.StatusUnauthorized)
			return
		}

		h := sha256.Sum256([]byte(token))
		tokenHash := hex.EncodeToString(h[:])

		device, err := m.store.GetDeviceByTokenHash(tokenHash)
		if err != nil || device == nil {
			http.Error(w, `{"error":{"code":"UNAUTHORIZED","message":"Unrecognized or revoked device token"}}`, http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), DeviceContextKey, device)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

