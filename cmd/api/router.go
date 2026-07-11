package main

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"github.com/mubeendevelops/convoy-chat/internal/auth"
	"github.com/mubeendevelops/convoy-chat/internal/config"
	"github.com/mubeendevelops/convoy-chat/internal/handlers"
	"github.com/mubeendevelops/convoy-chat/internal/store"
	"github.com/mubeendevelops/convoy-chat/internal/websocket"
)

// newRouter builds the full HTTP handler: middleware, health, the WebSocket
// upgrade endpoint, and every REST endpoint. Extracted from main so
// integration tests can construct the exact same router the real server
// runs, without duplicating its wiring.
func newRouter(cfg *config.Config, st *store.Store, wsServer *websocket.Server, logger *slog.Logger) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	// No middleware.RealIP: it's deprecated (GHSA-3fxj-6jh8-hvhx) — it
	// blindly trusts X-Forwarded-For/X-Real-IP, which lets a client spoof
	// its apparent RemoteAddr unless requests are guaranteed to pass through
	// a trusted proxy that sets those headers itself. Nothing here consumes
	// RemoteAddr today, so removing it costs nothing; a trusted-proxy-aware
	// replacement (chi's middleware.RealIPFrom or equivalent) belongs in
	// Phase 16 once the actual deploy topology (Railway/Render's edge) is
	// known, not guessed at here.
	r.Use(requestLogger(logger))
	r.Use(middleware.Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   cfg.CORSAllowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "Idempotency-Key"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	r.Get("/health", handlers.Health(st))

	// WebSocket connect authenticates via ?token= before the upgrade, so it
	// sits outside the Bearer-header auth middleware group below.
	r.Get("/ws", wsServer.Handler())

	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/auth/signup", handlers.Signup(st, cfg.JWTSecret, cfg.JWTTTL))
		r.Post("/auth/login", handlers.Login(st, cfg.JWTSecret, cfg.JWTTTL))
		// The refresh token itself is the credential here, so this
		// deliberately sits outside the Bearer-auth group below — an
		// expired access token is exactly the case this endpoint exists to
		// recover from.
		r.Post("/auth/refresh", handlers.Refresh(st, cfg.JWTSecret, cfg.JWTTTL))

		r.Group(func(r chi.Router) {
			r.Use(auth.Middleware(cfg.JWTSecret))
			r.Post("/auth/logout", handlers.Logout(st))
			r.Get("/users/search", handlers.SearchUsers(st))
			r.Get("/users/{user_id}", handlers.GetUser(st))

			r.Post("/rooms", handlers.CreateRoom(st))
			r.Get("/rooms", handlers.ListRooms(st))
			r.Get("/rooms/public", handlers.ListPublicChannels(st))
			r.Get("/rooms/{room_id}", handlers.GetRoom(st))
			r.Get("/rooms/{room_id}/members", handlers.ListRoomMembers(st))
			r.Get("/rooms/{room_id}/presence", handlers.RoomPresence(st))
			r.Post("/rooms/{room_id}/invite", handlers.InviteMember(st))
			r.Post("/rooms/{room_id}/join", handlers.JoinChannel(st, logger))
			r.Post("/rooms/{room_id}/leave", handlers.LeaveRoom(st, logger))
			r.With(handlers.RequireRoomAdmin(st)).Patch("/rooms/{room_id}/members/{user_id}/role", handlers.ChangeMemberRole(st, logger))
			r.With(handlers.RequireRoomAdmin(st)).Delete("/rooms/{room_id}/members/{user_id}", handlers.RemoveMember(st, logger))

			r.Get("/rooms/{room_id}/messages", handlers.ListMessages(st))
			r.Post("/rooms/{room_id}/messages", handlers.SendMessage(st))
			r.Patch("/messages/{message_id}", handlers.EditMessage(st, logger))
			r.Delete("/messages/{message_id}", handlers.DeleteMessage(st, logger))
			r.Post("/messages/{message_id}/reactions", handlers.ToggleReaction(st, logger))

			r.With(handlers.RequireSystemAdmin(st)).Get("/admin/rooms", handlers.ListAllRooms(st))
			r.With(handlers.RequireSystemAdmin(st)).Get("/admin/presence", handlers.ListAllUserPresence(st))
		})
	})

	return r
}
