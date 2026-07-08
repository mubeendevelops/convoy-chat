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
