package models

import (
	"time"

	"github.com/google/uuid"
)

// RefreshToken mirrors the refresh_tokens table. It is never serialized into
// an API response — the plaintext token value (which this struct never even
// holds; only its hash does) leaves the server exactly once, at issuance.
type RefreshToken struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	TokenHash string
	FamilyID  uuid.UUID
	CreatedAt time.Time
	ExpiresAt time.Time
	RevokedAt *time.Time
}
