// Package testutil provides shared integration-test infrastructure: fresh,
// isolated Postgres + Redis testcontainers (same images as
// docker-compose.yml, for dev/test parity), migrated and ready to use. It's
// imported only from _test.go files, never from production code.
package testutil

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/mubeendevelops/convoy-chat/internal/store"
)

const containerStartupTimeout = 60 * time.Second

// migrationsDir resolves migrations/ relative to this file's own source
// location (via runtime.Caller), not the test binary's working directory —
// so it works the same regardless of which package's test imports NewStore.
func migrationsDir() (string, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("resolving testutil source location")
	}
	return filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations"))
}

// requireDocker skips the test (rather than failing) when the Docker daemon
// isn't reachable, so `go test ./...` doesn't hard-fail on a machine without
// Docker — only the tests that actually need it are skipped.
func requireDocker(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "info").Run(); err != nil {
		t.Skip("skipping: docker is not available")
	}
}

// NewStore spins up fresh Postgres and Redis containers, applies every
// migration, and returns a ready *store.Store. Both containers and the
// store's connections are torn down automatically via t.Cleanup — callers
// don't need to close anything themselves.
func NewStore(t *testing.T) *store.Store {
	t.Helper()
	requireDocker(t)

	ctx := context.Background()

	pgContainer, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("convoychat_test"),
		tcpostgres.WithUsername("convoy"),
		tcpostgres.WithPassword("convoy"),
		testcontainers.WithWaitStrategy(
			// The postgres image logs this line twice (once for the initdb
			// bootstrap run, once for the real server) — waiting for a single
			// occurrence is a well-known flaky-test trap.
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(containerStartupTimeout),
		),
	)
	if err != nil {
		t.Fatalf("starting postgres testcontainer: %v", err)
	}
	t.Cleanup(func() {
		if err := pgContainer.Terminate(context.Background()); err != nil {
			t.Logf("terminating postgres testcontainer: %v", err)
		}
	})

	dbURL, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("getting postgres connection string: %v", err)
	}

	migDir, err := migrationsDir()
	if err != nil {
		t.Fatalf("resolving migrations dir: %v", err)
	}
	m, err := migrate.New("file://"+migDir, dbURL)
	if err != nil {
		t.Fatalf("initializing migrator: %v", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("applying migrations: %v", err)
	}

	redisContainer, err := tcredis.Run(ctx, "redis:7-alpine",
		testcontainers.WithWaitStrategy(
			wait.ForLog("Ready to accept connections").WithStartupTimeout(containerStartupTimeout),
		),
	)
	if err != nil {
		t.Fatalf("starting redis testcontainer: %v", err)
	}
	t.Cleanup(func() {
		if err := redisContainer.Terminate(context.Background()); err != nil {
			t.Logf("terminating redis testcontainer: %v", err)
		}
	})

	redisURL, err := redisContainer.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("getting redis connection string: %v", err)
	}

	db, err := store.NewPostgresPool(ctx, dbURL)
	if err != nil {
		t.Fatalf("connecting to test postgres: %v", err)
	}
	t.Cleanup(db.Close)

	rdb, err := store.NewRedisClient(ctx, redisURL)
	if err != nil {
		t.Fatalf("connecting to test redis: %v", err)
	}
	t.Cleanup(func() {
		_ = rdb.Close()
	})

	return store.New(db, rdb)
}
