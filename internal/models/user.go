package models

import (
	"time"

	"github.com/google/uuid"
)

// User mirrors the users table. PasswordHash is tagged json:"-" so it can
// never be serialized into an API response, no matter which handler returns
// a User value.
type User struct {
	ID           uuid.UUID `json:"id"`
	Username     string    `json:"username"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	AvatarURL    *string   `json:"avatar_url,omitempty"`
	Bio          *string   `json:"bio,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}
