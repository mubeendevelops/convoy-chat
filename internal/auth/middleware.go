package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/mubeendevelops/convoy-chat/internal/httpx"
)

type contextKey int

const userIDContextKey contextKey = iota

// Middleware validates the Authorization: Bearer <token> header and injects
// the authenticated user's ID into the request context. It rejects the
// request with 401 before next is ever called if the token is missing,
// malformed, expired, or invalid.
func Middleware(secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			if !ok {
				httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing or malformed Authorization header")
				return
			}

			claims, err := ValidateToken(token, secret)
			if err != nil {
				httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid or expired token")
				return
			}

			userID, err := UserIDFromClaims(claims)
			if err != nil {
				httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid or expired token")
				return
			}

			ctx := context.WithValue(r.Context(), userIDContextKey, userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func bearerToken(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, prefix) {
		return "", false
	}
	token := strings.TrimPrefix(header, prefix)
	if token == "" {
		return "", false
	}
	return token, true
}

// UserIDFromContext returns the authenticated user ID injected by Middleware.
func UserIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(userIDContextKey).(uuid.UUID)
	return id, ok
}
