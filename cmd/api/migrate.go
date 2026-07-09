package main

import (
	"errors"
	"log/slog"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"

	"github.com/mubeendevelops/convoy-chat/internal/config"
)

// runMigrations applies every pending migration and exits, without starting
// the HTTP server, WebSocket hub, or Redis broker. It's the `-migrate` mode
// (see main.go): a one-shot init step meant to run once per deploy, ahead of
// the server starting — a platform's pre-deploy/release command, or
// docker-compose.prod.yml's migrate service — never called from the normal
// serve path. See CLAUDE.md's migration-on-deploy strategy for why this is a
// separate step rather than migrating automatically on every server boot.
//
// Uses the same golang-migrate library (not the CLI) as internal/testutil,
// which manages its own database connection from the DSN directly rather
// than sharing the server's pgx pool.
func runMigrations(cfg *config.Config, logger *slog.Logger) int {
	m, err := migrate.New("file://"+cfg.MigrationsPath, cfg.DatabaseURL)
	if err != nil {
		logger.Error("initializing migrator", "error", err)
		return 1
	}
	defer func() {
		srcErr, dbErr := m.Close()
		if srcErr != nil || dbErr != nil {
			logger.Warn("closing migrator", "source_error", srcErr, "db_error", dbErr)
		}
	}()

	if err := m.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			logger.Info("migrations already up to date")
			return 0
		}
		logger.Error("applying migrations", "error", err)
		return 1
	}

	logger.Info("migrations applied successfully")
	return 0
}
