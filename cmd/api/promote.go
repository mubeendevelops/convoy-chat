package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/mubeendevelops/convoy-chat/internal/config"
	"github.com/mubeendevelops/convoy-chat/internal/store"
)

// runPromoteAdmin grants email system-admin status and exits, without
// starting the HTTP server, WebSocket hub, or Redis broker — the
// `-promote-admin` mode (see main.go), the one-shot bootstrap step for
// granting the *first* system admin (no endpoint can do this, since granting
// one requires already being one — see plan.md's admin-dashboard proposal).
// Same "separate one-shot mode, same binary" spirit as `-migrate`, though
// unlike that mode this does go through the normal store.Store (Postgres +
// Redis) construction: ALL database access goes through internal/store per
// convention, and PromoteToSystemAdmin is a store method like any other.
func runPromoteAdmin(cfg *config.Config, logger *slog.Logger, email string) int {
	ctx := context.Background()

	db, err := store.NewPostgresPool(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("connecting to postgres", "error", err)
		return 1
	}
	defer db.Close()

	rdb, err := store.NewRedisClient(ctx, cfg.RedisURL)
	if err != nil {
		logger.Error("connecting to redis", "error", err)
		return 1
	}
	defer func() { _ = rdb.Close() }()

	st := store.New(db, rdb)

	user, err := st.PromoteToSystemAdmin(ctx, email)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			logger.Error("no user found with that email", "email", email)
			return 1
		}
		logger.Error("promoting user to system admin", "email", email, "error", err)
		return 1
	}

	logger.Info("promoted user to system admin", "user_id", user.ID, "username", user.Username, "email", email)
	return 0
}
