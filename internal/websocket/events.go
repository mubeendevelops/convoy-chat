package websocket

import (
	"time"

	"github.com/google/uuid"

	"github.com/mubeendevelops/convoy-chat/internal/models"
)

// Client → server event type tags.
const (
	eventRoomJoin       = "room.join"
	eventRoomLeave      = "room.leave"
	eventMessageSend    = "message.send"
	eventTypingStart    = "typing.start"
	eventTypingStop     = "typing.stop"
	eventPresenceUpdate = "presence.update"
)

// Server → client event type tags.
const (
	eventError             = "error"
	eventMessageNew        = "message.new"
	eventUserJoined        = "user.joined"
	eventUserLeft          = "user.left"
	eventUserTyping        = "user.typing"
	eventUserStatusChanged = "user.status_changed"
)

// inboundEnvelope decodes a client frame. type + room_id route every event;
// content + message_type are only read for message.send; status is only read
// for presence.update. Read-receipt/reaction events add their own fields in
// Phase 7.
type inboundEnvelope struct {
	Type        string `json:"type"`
	RoomID      string `json:"room_id"`
	Content     string `json:"content"`
	MessageType string `json:"message_type"`
	Status      string `json:"status"`
}

// errorEvent is the server's {"type":"error","code","message"} envelope.
type errorEvent struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// messageNewEvent and its payload match the message.new shape in
// ConvoyChat_Complete_Context.md exactly: {id, room_id, user{id,username,
// avatar_url}, content, created_at, read_by}. read_by is always [] until read
// receipts land (Phase 7); message_type/updated_at are intentionally omitted
// (available via the REST history shape if a client needs them).
type messageNewEvent struct {
	Type    string            `json:"type"`
	Message messageNewPayload `json:"message"`
}

type messageNewPayload struct {
	ID        uuid.UUID          `json:"id"`
	RoomID    uuid.UUID          `json:"room_id"`
	User      models.UserSummary `json:"user"`
	Content   *string            `json:"content"`
	CreatedAt time.Time          `json:"created_at"`
	ReadBy    []uuid.UUID        `json:"read_by"`
}

// userRef is the {id, username} pair embedded in user.joined.
type userRef struct {
	ID       uuid.UUID `json:"id"`
	Username string    `json:"username"`
}

// userJoinedEvent matches {"type":"user.joined","user":{"id","username"},"room_id"}.
type userJoinedEvent struct {
	Type   string    `json:"type"`
	User   userRef   `json:"user"`
	RoomID uuid.UUID `json:"room_id"`
}

// userLeftEvent matches {"type":"user.left","user_id","room_id"}.
type userLeftEvent struct {
	Type   string    `json:"type"`
	UserID uuid.UUID `json:"user_id"`
	RoomID uuid.UUID `json:"room_id"`
}

// userTypingEvent matches {"type":"user.typing","user_id","room_id","is_typing"}.
// is_typing is an addition beyond the context file's shape (recorded in
// plan.md Decisions): the documented shape has no way to distinguish a start
// from a stop, so a receiver can't tell "still typing, keep waiting" from
// "stopped" without it — needed for typing.stop, and for the server-side
// auto-expire of a dropped stop to be observable at all.
type userTypingEvent struct {
	Type     string    `json:"type"`
	UserID   uuid.UUID `json:"user_id"`
	RoomID   uuid.UUID `json:"room_id"`
	IsTyping bool      `json:"is_typing"`
}

// userStatusChangedEvent matches
// {"type":"user.status_changed","user_id","status","last_seen_at"}.
type userStatusChangedEvent struct {
	Type       string                `json:"type"`
	UserID     uuid.UUID             `json:"user_id"`
	Status     models.PresenceStatus `json:"status"`
	LastSeenAt time.Time             `json:"last_seen_at"`
}
