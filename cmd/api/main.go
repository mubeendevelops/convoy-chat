package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mubeendevelops/convoy-chat/internal/config"
	"github.com/mubeendevelops/convoy-chat/internal/store"
	"github.com/mubeendevelops/convoy-chat/internal/websocket"
)

const shutdownTimeout = 10 * time.Second

// main defers all exit-code decisions to run, so run can use ordinary
// `return` on every path — including the early-failure ones — and let its
// defers (signal-handler cleanup, closing the store) actually execute.
// os.Exit bypasses deferred calls, so calling it directly from deep inside
// the setup logic (the original shape of this function) silently skipped
// them on every non-happy-path exit.
func main() {
	os.Exit(run())
}

func run() int {
	migrateOnly := flag.Bool("migrate", false, "apply pending database migrations, then exit (no server)")
	promoteAdminEmail := flag.String("promote-admin", "", "grant system-admin status to the user with this email, then exit (no server)")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "loading config:", err)
		return 1
	}

	logger := newLogger(cfg.AppEnv)

	if *migrateOnly {
		return runMigrations(cfg, logger)
	}
	if *promoteAdminEmail != "" {
		return runPromoteAdmin(cfg, logger, *promoteAdminEmail)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.NewPostgresPool(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("connecting to postgres", "error", err)
		return 1
	}

	rdb, err := store.NewRedisClient(ctx, cfg.RedisURL)
	if err != nil {
		logger.Error("connecting to redis", "error", err)
		db.Close()
		return 1
	}

	st := store.New(db, rdb)
	defer st.Close()

	// The real-time layer: Hub (connection state, single goroutine) + Broker
	// (Redis Pub/Sub fan-out). Both stop when ctx is cancelled (graceful
	// shutdown).
	wsServer := websocket.NewServer(st, cfg.JWTSecret, cfg.CORSAllowedOrigins, logger)
	wsServer.Run(ctx)

	r := newRouter(cfg, st, wsServer, logger)

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
			return 1
		}
	case <-ctx.Done():
		logger.Info("shutting down")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("graceful shutdown failed", "error", err)
		}
	}
	return 0
}
