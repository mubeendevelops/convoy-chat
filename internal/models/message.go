package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type MessageType string

const (
	MessageTypeText   MessageType = "text"
	MessageTypeImage  MessageType = "image"
	MessageTypeFile   MessageType = "file"
	MessageTypeSystem MessageType = "system"
)

// Message mirrors the messages table.
type Message struct {
	ID          uuid.UUID       `json:"id"`
	RoomID      uuid.UUID       `json:"room_id"`
	UserID      uuid.UUID       `json:"user_id"`
	Content     string          `json:"content"`
	MessageType MessageType     `json:"message_type"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
	EditedAt    *time.Time      `json:"edited_at,omitempty"`
	DeletedAt   *time.Time      `json:"deleted_at,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// MessageWithAuthor is a message row joined with the author's public user
// summary, for history responses and message-send responses. Content is nil
// when the message has been soft-deleted; callers render a placeholder
// rather than the original text, which is retained in the database but
// never served back through this shape. ReadBy/Reactions reflect existing
// read/reaction activity even on a deleted message — that activity happened
// and isn't masked, unlike the message text itself. EditedAt is nil unless
// the message has been edited (PATCH /messages/{id}, author-only) at least
// once — the frontend's "(edited)" indicator keys off its presence.
type MessageWithAuthor struct {
	ID          uuid.UUID                `json:"id"`
	RoomID      uuid.UUID                `json:"room_id"`
	User        UserSummary              `json:"user"`
	Content     *string                  `json:"content"`
	MessageType MessageType              `json:"message_type"`
	EditedAt    *time.Time               `json:"edited_at,omitempty"`
	DeletedAt   *time.Time               `json:"deleted_at,omitempty"`
	CreatedAt   time.Time                `json:"created_at"`
	UpdatedAt   time.Time                `json:"updated_at"`
	ReadBy      []uuid.UUID              `json:"read_by"`
	Reactions   []MessageReactionSummary `json:"reactions"`
}

// MessageReactionSummary groups a message's reactions by emoji, e.g. "👍 3"
// rather than three separate rows — the shape most chat UIs render directly.
// UserIDs is ordered by when each user reacted.
type MessageReactionSummary struct {
	Emoji   string      `json:"emoji"`
	Count   int         `json:"count"`
	UserIDs []uuid.UUID `json:"user_ids"`
}
