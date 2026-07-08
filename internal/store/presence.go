package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/mubeendevelops/convoy-chat/internal/models"
)

const (
	presenceStatusPrefix = "presence:status:"
	presenceConnsPrefix  = "presence:conns:"

	// connsSafetyTTL bounds the connection counter so a server process that
	// crashes mid-connection (no chance to run its disconnect path) doesn't
	// leak it forever. Ordinary heartbeats refresh this well before it lapses;
	// it exists purely as a crash backstop, not for routine correctness.
	connsSafetyTTL = 24 * time.Hour
)

func presenceStatusKey(userID uuid.UUID) string { return presenceStatusPrefix + userID.String() }
func presenceConnsKey(userID uuid.UUID) string  { return presenceConnsPrefix + userID.String() }

// PresenceConnect records a new live connection for userID, across all server
// instances (the counter lives in Redis, shared). wentOnline is true only on
// the 0→1 transition — the case where the caller should broadcast
// user.status_changed(online). A second/later connection (another tab or
// device) just extends the existing status key's TTL without changing its
// value, so an explicit "away" survives a new tab connecting.
func (s *Store) PresenceConnect(ctx context.Context, userID uuid.UUID, ttl time.Duration) (wentOnline bool, err error) {
	count, err := s.Redis.Incr(ctx, presenceConnsKey(userID)).Result()
	if err != nil {
		return false, fmt.Errorf("incrementing presence connection count: %w", err)
	}
	if err := s.Redis.Expire(ctx, presenceConnsKey(userID), connsSafetyTTL).Err(); err != nil {
		return false, fmt.Errorf("setting presence connection count ttl: %w", err)
	}

	if count > 1 {
		if err := s.Redis.Expire(ctx, presenceStatusKey(userID), ttl).Err(); err != nil {
			return false, fmt.Errorf("refreshing presence status ttl: %w", err)
		}
		return false, nil
	}

	if err := s.Redis.Set(ctx, presenceStatusKey(userID), string(models.PresenceOnline), ttl).Err(); err != nil {
		return false, fmt.Errorf("setting presence status: %w", err)
	}
	if err := s.upsertUserPresence(ctx, userID, models.PresenceOnline); err != nil {
		return false, err
	}
	return true, nil
}

// PresenceDisconnect records a connection going away. wentOffline is true
// only when this was the user's last live connection anywhere — the case
// where the caller should broadcast user.status_changed(offline).
func (s *Store) PresenceDisconnect(ctx context.Context, userID uuid.UUID) (wentOffline bool, err error) {
	count, err := s.Redis.Decr(ctx, presenceConnsKey(userID)).Result()
	if err != nil {
		return false, fmt.Errorf("decrementing presence connection count: %w", err)
	}
	if count > 0 {
		return false, nil
	}

	if count < 0 {
		// A prior crash likely leaked an increment; never let the counter sit
		// negative and confuse the next connect's 0→1 detection.
		if err := s.Redis.Set(ctx, presenceConnsKey(userID), 0, connsSafetyTTL).Err(); err != nil {
			return false, fmt.Errorf("resetting presence connection count: %w", err)
		}
	}

	if err := s.Redis.Del(ctx, presenceStatusKey(userID)).Err(); err != nil {
		return false, fmt.Errorf("clearing presence status: %w", err)
	}
	if err := s.upsertUserPresence(ctx, userID, models.PresenceOffline); err != nil {
		return false, err
	}
	return true, nil
}

// PresenceHeartbeat refreshes TTLs for a still-live connection. It's
// Redis-only — nothing about the user's state changed, only that they're
// still here, so there's no reason to write user_presence on every beat. If
// the status key already lapsed (e.g. a scheduling delay pushed this
// heartbeat past ttl) it's resurrected as online rather than leaving the user
// stuck showing offline until their next reconnect.
func (s *Store) PresenceHeartbeat(ctx context.Context, userID uuid.UUID, ttl time.Duration) error {
	refreshed, err := s.Redis.Expire(ctx, presenceStatusKey(userID), ttl).Result()
	if err != nil {
		return fmt.Errorf("refreshing presence status ttl: %w", err)
	}
	if !refreshed {
		if err := s.Redis.Set(ctx, presenceStatusKey(userID), string(models.PresenceOnline), ttl).Err(); err != nil {
			return fmt.Errorf("resurrecting presence status: %w", err)
		}
	}
	if err := s.Redis.Expire(ctx, presenceConnsKey(userID), connsSafetyTTL).Err(); err != nil {
		return fmt.Errorf("refreshing presence connection count ttl: %w", err)
	}
	return nil
}

// PresenceSetStatus explicitly sets userID's visible status (presence.update),
// independent of their live connection count — a connected user can mark
// themselves "away", or even "offline" while remaining connected.
func (s *Store) PresenceSetStatus(ctx context.Context, userID uuid.UUID, status models.PresenceStatus, ttl time.Duration) error {
	if err := s.Redis.Set(ctx, presenceStatusKey(userID), string(status), ttl).Err(); err != nil {
		return fmt.Errorf("setting presence status: %w", err)
	}
	return s.upsertUserPresence(ctx, userID, status)
}

func (s *Store) upsertUserPresence(ctx context.Context, userID uuid.UUID, status models.PresenceStatus) error {
	const q = `
		INSERT INTO user_presence (user_id, status, last_seen_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (user_id) DO UPDATE
			SET status = EXCLUDED.status, last_seen_at = NOW()`
	if _, err := s.DB.Exec(ctx, q, userID, status); err != nil {
		return fmt.Errorf("upserting user presence: %w", err)
	}
	return nil
}
