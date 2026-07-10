package models

import (
	"time"

	"github.com/google/uuid"
)

// User mirrors the users table. PasswordHash is tagged json:"-" so it can
// never be serialized into an API response, no matter which handler returns
// a User value. IsSystemAdmin is safe to serialize (unlike PasswordHash) —
// the frontend needs it to decide whether to render admin-only UI; the
// server remains the real gate on every admin endpoint regardless.
type User struct {
	ID            uuid.UUID `json:"id"`
	Username      string    `json:"username"`
	Email         string    `json:"email"`
	PasswordHash  string    `json:"-"`
	AvatarURL     *string   `json:"avatar_url,omitempty"`
	Bio           *string   `json:"bio,omitempty"`
	IsSystemAdmin bool      `json:"is_system_admin"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// UserSummary is the safe, minimal view of a user embedded in other API and
// WebSocket payloads (room members, message authors, presence events).
type UserSummary struct {
	ID        uuid.UUID `json:"id"`
	Username  string    `json:"username"`
	AvatarURL *string   `json:"avatar_url,omitempty"`
}
