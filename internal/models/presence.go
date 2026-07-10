package models

import (
	"time"

	"github.com/google/uuid"
)

type PresenceStatus string

const (
	PresenceOnline  PresenceStatus = "online"
	PresenceAway    PresenceStatus = "away"
	PresenceOffline PresenceStatus = "offline"
)

// UserPresence mirrors the user_presence table: a durable snapshot written on
// every transition purely so "last seen" can be shown for an offline user.
// Redis (TTL keys + heartbeat, see store/presence.go) is the source of truth
// for "is this user online right now".
type UserPresence struct {
	UserID     uuid.UUID      `json:"user_id"`
	Status     PresenceStatus `json:"status"`
	LastSeenAt *time.Time     `json:"last_seen_at,omitempty"`
}

// AdminPresenceEntry is the system-admin-only "every user's current
// presence" snapshot shape (GET /admin/presence) — includes the username
// (UserPresence doesn't) since the dashboard has no separate user list to
// join against.
type AdminPresenceEntry struct {
	UserID     uuid.UUID      `json:"user_id"`
	Username   string         `json:"username"`
	Status     PresenceStatus `json:"status"`
	LastSeenAt *time.Time     `json:"last_seen_at,omitempty"`
}
