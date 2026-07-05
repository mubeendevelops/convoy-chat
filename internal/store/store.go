package store

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Store is the single point of access for all database and cache operations.
// Handlers and the websocket package depend on this type rather than talking
// to pgx or go-redis directly.
type Store struct {
	DB    *pgxpool.Pool
	Redis *redis.Client
}

func New(db *pgxpool.Pool, redisClient *redis.Client) *Store {
	return &Store{DB: db, Redis: redisClient}
}

func (s *Store) PingPostgres(ctx context.Context) error {
	return s.DB.Ping(ctx)
}

func (s *Store) PingRedis(ctx context.Context) error {
	return s.Redis.Ping(ctx).Err()
}

// Close releases the Postgres pool and Redis connection. Safe to call once
// during graceful shutdown.
func (s *Store) Close() {
	s.DB.Close()
	_ = s.Redis.Close()
}
