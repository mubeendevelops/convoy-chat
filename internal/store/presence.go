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

// ListAllUserPresence returns a system-wide snapshot: every registered user
// with their current presence status, defaulting to "offline" for anyone
// with no live Redis entry (same "no live signal yet = offline" default
// already used by the frontend's usePresence.ts, just computed server-side
// and system-wide instead of per-room) — for the system-admin dashboard
// (GET /admin/presence). One Redis MGET across every user's status key, not
// N round trips. O(all users), no pagination — a known, flagged non-scaling
// shortcut (see plan.md's admin-dashboard proposal), fine at this app's
// scale, same spirit as RoomsList's N+1 direct-room lookups.
func (s *Store) ListAllUserPresence(ctx context.Context) ([]models.AdminPresenceEntry, error) {
	const q = `
		SELECT u.id, u.username, up.last_seen_at
		FROM users u
		LEFT JOIN user_presence up ON up.user_id = u.id
		ORDER BY u.username ASC`

	rows, err := s.DB.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("querying users for presence snapshot: %w", err)
	}

	entries := make([]models.AdminPresenceEntry, 0)
	keys := make([]string, 0)
	for rows.Next() {
		var e models.AdminPresenceEntry
		if err := rows.Scan(&e.UserID, &e.Username, &e.LastSeenAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scanning presence entry: %w", err)
		}
		e.Status = models.PresenceOffline
		entries = append(entries, e)
		keys = append(keys, presenceStatusKey(e.UserID))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterating users for presence snapshot: %w", err)
	}
	rows.Close()

	if len(keys) == 0 {
		return entries, nil
	}

	// MGet preserves key order, so statuses[i] corresponds to entries[i]/keys[i].
	statuses, err := s.Redis.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("fetching presence statuses: %w", err)
	}
	for i, raw := range statuses {
		if str, ok := raw.(string); ok && str != "" {
			entries[i].Status = models.PresenceStatus(str)
		}
	}

	return entries, nil
}

// ListPresenceForRoom returns the current presence of every active member of
// roomID, defaulting "offline" for anyone with no live Redis entry — the same
// one-query-plus-one-MGET shape as ListAllUserPresence, just scoped to a room.
// Backs GET /rooms/{id}/presence, which lets a client hydrate a peer's status
// on room open rather than only learning it from a live event received *after*
// subscribing (the frontend presence store has no other way to know a member
// who went online before this session's socket connected).
func (s *Store) ListPresenceForRoom(ctx context.Context, roomID uuid.UUID) ([]models.UserPresence, error) {
	const q = `
		SELECT m.user_id, up.last_seen_at
		FROM room_members m
		LEFT JOIN user_presence up ON up.user_id = m.user_id
		WHERE m.room_id = $1 AND m.left_at IS NULL`

	rows, err := s.DB.Query(ctx, q, roomID)
	if err != nil {
		return nil, fmt.Errorf("querying room members for presence snapshot: %w", err)
	}

	entries := make([]models.UserPresence, 0)
	keys := make([]string, 0)
	for rows.Next() {
		var e models.UserPresence
		if err := rows.Scan(&e.UserID, &e.LastSeenAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scanning room presence entry: %w", err)
		}
		e.Status = models.PresenceOffline
		entries = append(entries, e)
		keys = append(keys, presenceStatusKey(e.UserID))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterating room members for presence snapshot: %w", err)
	}
	rows.Close()

	if len(keys) == 0 {
		return entries, nil
	}

	// MGet preserves key order, so statuses[i] corresponds to entries[i]/keys[i].
	statuses, err := s.Redis.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("fetching room presence statuses: %w", err)
	}
	for i, raw := range statuses {
		if str, ok := raw.(string); ok && str != "" {
			entries[i].Status = models.PresenceStatus(str)
		}
	}

	return entries, nil
}
