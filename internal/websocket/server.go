package websocket

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	gws "github.com/gorilla/websocket"

	"github.com/mubeendevelops/convoy-chat/internal/auth"
	"github.com/mubeendevelops/convoy-chat/internal/httpx"
	"github.com/mubeendevelops/convoy-chat/internal/store"
)

// dbTimeout bounds a store call made while handling a WebSocket frame. Unlike a
// REST handler there's no request context to inherit, so we derive a bounded
// one from the server's run context (cancelled on shutdown).
const dbTimeout = 5 * time.Second

// Server wires the real-time layer's shared dependencies (store, Hub, Redis
// Broker) and produces the GET /ws handler. One per process. It owns the
// inbound event dispatch (see handlers.go).
type Server struct {
	store   *store.Store
	hub     *Hub
	broker  *Broker
	secret  string
	origins []string
	logger  *slog.Logger

	// runCtx is the base context for store calls made while dispatching frames;
	// set by Run before any connection is accepted.
	runCtx context.Context
}

// NewServer constructs the real-time server and wires the Hub ↔ Broker pair.
// Call Run to start their goroutines, then mount Handler at GET /ws.
func NewServer(st *store.Store, secret string, origins []string, logger *slog.Logger) *Server {
	hub := NewHub(logger)
	broker := NewBroker(st, hub, logger)
	hub.SetSubscriber(broker)
	return &Server{
		store:   st,
		hub:     hub,
		broker:  broker,
		secret:  secret,
		origins: origins,
		logger:  logger,
		runCtx:  context.Background(),
	}
}

// Run starts the Hub and Broker goroutines. They stop when ctx is cancelled.
func (s *Server) Run(ctx context.Context) {
	s.runCtx = ctx
	go s.hub.Run(ctx)
	go s.broker.Run(ctx)
}

// Handler returns the GET /ws endpoint. It authenticates via the ?token= query
// parameter and checks the request Origin *before* upgrading, so a missing/bad
// token (401) or a disallowed origin (403) gets a normal HTTP error instead of
// a broken WebSocket handshake. The token rides in the query string because the
// browser WebSocket API can't set an Authorization header — a documented v1
// tradeoff (plan.md decision 6).
func (s *Server) Handler() http.HandlerFunc {
	upgrader := gws.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     originChecker(s.origins),
	}

	return func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("token")
		if token == "" {
			httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing token query parameter")
			return
		}

		claims, err := auth.ValidateToken(token, s.secret)
		if err != nil {
			httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid or expired token")
			return
		}
		userID, err := auth.UserIDFromClaims(claims)
		if err != nil {
			httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid or expired token")
			return
		}

		// Cache the username once, for user.joined announcements. A valid token
		// for a since-deleted user is treated as unauthorized.
		ctx, cancel := context.WithTimeout(s.runCtx, dbTimeout)
		defer cancel()
		user, err := s.store.GetUserByID(ctx, userID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid or expired token")
				return
			}
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to load user")
			return
		}

		// Upgrade writes its own HTTP error response on failure (e.g. 403 when
		// CheckOrigin rejects the origin), so there's nothing to write here.
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			s.logger.Warn("ws upgrade failed", "user_id", userID, "error", err)
			return
		}

		client := newClient(s, conn, userID, user.Username)
		s.hub.Register(client)
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
