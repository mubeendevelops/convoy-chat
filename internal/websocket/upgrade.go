package websocket

import (
	"log/slog"
	"net/http"

	gws "github.com/gorilla/websocket"

	"github.com/mubeendevelops/convoy-chat/internal/auth"
	"github.com/mubeendevelops/convoy-chat/internal/httpx"
)

// Handler returns the GET /ws endpoint. It authenticates via the ?token= query
// parameter and checks the request Origin *before* upgrading, so a missing/bad
// token (401) or a disallowed origin (403) gets a normal HTTP error instead of
// a broken WebSocket handshake. Only after both pass do we upgrade and hand the
// connection to the Hub. The token rides in the query string (not a header)
// because the browser WebSocket API can't set Authorization — a documented v1
// tradeoff (see plan.md decision 6).
func Handler(hub *Hub, secret string, allowedOrigins []string, logger *slog.Logger) http.HandlerFunc {
	upgrader := gws.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     originChecker(allowedOrigins),
	}

	return func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("token")
		if token == "" {
			httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing token query parameter")
			return
		}

		claims, err := auth.ValidateToken(token, secret)
		if err != nil {
			httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid or expired token")
			return
		}
		userID, err := auth.UserIDFromClaims(claims)
		if err != nil {
			httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid or expired token")
			return
		}

		// Upgrade writes its own HTTP error response on failure (e.g. 403 when
		// CheckOrigin rejects the origin), so there's nothing to write here.
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			logger.Warn("ws upgrade failed", "user_id", userID, "error", err)
			return
		}

		client := newClient(hub, conn, userID, logger)
		hub.Register(client)
		go client.writePump()
		go client.readPump()
	}
}

// originChecker mirrors CORS_ALLOWED_ORIGINS for the WebSocket handshake: it
// admits browser clients only from the configured origins, while still allowing
// non-browser clients (curl, websocat, native apps) that send no Origin header.
// Without this, gorilla's default rejects any cross-origin browser handshake.
func originChecker(allowed []string) func(*http.Request) bool {
	set := make(map[string]struct{}, len(allowed))
	for _, o := range allowed {
		set[o] = struct{}{}
	}
	return func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true // non-browser client
		}
		_, ok := set[origin]
		return ok
	}
}
