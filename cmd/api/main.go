package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"github.com/mubeendevelops/convoy-chat/internal/auth"
	"github.com/mubeendevelops/convoy-chat/internal/config"
	"github.com/mubeendevelops/convoy-chat/internal/handlers"
	"github.com/mubeendevelops/convoy-chat/internal/store"
	"github.com/mubeendevelops/convoy-chat/internal/websocket"
)

const shutdownTimeout = 10 * time.Second

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "loading config:", err)
		os.Exit(1)
	}

	logger := newLogger(cfg.AppEnv)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.NewPostgresPool(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("connecting to postgres", "error", err)
		os.Exit(1)
	}

	rdb, err := store.NewRedisClient(ctx, cfg.RedisURL)
	if err != nil {
		logger.Error("connecting to redis", "error", err)
		db.Close()
		os.Exit(1)
	}

	st := store.New(db, rdb)
	defer st.Close()

	// The Hub owns all real-time connection state in a single goroutine; it
	// stops when ctx is cancelled (graceful shutdown).
	hub := websocket.NewHub(logger)
	go hub.Run(ctx)

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(requestLogger(logger))
	r.Use(middleware.Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   cfg.CORSAllowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	r.Get("/health", handlers.Health(st))

	// WebSocket connect authenticates via ?token= before the upgrade, so it
	// sits outside the Bearer-header auth middleware group below.
	r.Get("/ws", websocket.Handler(hub, cfg.JWTSecret, cfg.CORSAllowedOrigins, logger))

	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/auth/signup", handlers.Signup(st, cfg.JWTSecret, cfg.JWTTTL))
		r.Post("/auth/login", handlers.Login(st, cfg.JWTSecret, cfg.JWTTTL))

		r.Group(func(r chi.Router) {
			r.Use(auth.Middleware(cfg.JWTSecret))
			r.Get("/users/{user_id}", handlers.GetUser(st))

			r.Post("/rooms", handlers.CreateRoom(st))
			r.Get("/rooms", handlers.ListRooms(st))
			r.Get("/rooms/{room_id}", handlers.GetRoom(st))
			r.Get("/rooms/{room_id}/members", handlers.ListRoomMembers(st))
			r.Post("/rooms/{room_id}/invite", handlers.InviteMember(st))
			r.Post("/rooms/{room_id}/leave", handlers.LeaveRoom(st))

			r.Get("/rooms/{room_id}/messages", handlers.ListMessages(st))
			r.Post("/rooms/{room_id}/messages", handlers.SendMessage(st))
			r.Delete("/messages/{message_id}", handlers.DeleteMessage(st))
		})
	})

	srv := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("starting server", "addr", cfg.Addr(), "env", cfg.AppEnv)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	select {
	case err := <-serverErr:
		if err != nil {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		logger.Info("shutting down")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("graceful shutdown failed", "error", err)
		}
	}
}
