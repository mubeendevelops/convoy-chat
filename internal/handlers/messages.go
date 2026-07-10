package handlers

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mubeendevelops/convoy-chat/internal/auth"
	"github.com/mubeendevelops/convoy-chat/internal/httpx"
	"github.com/mubeendevelops/convoy-chat/internal/models"
	"github.com/mubeendevelops/convoy-chat/internal/store"
)

const (
	defaultMessageLimit  = 50
	maxMessageLimit      = 100
	maxMessageContentLen = 10000
)

var (
	errInvalidLimit       = errors.New("limit must be an integer between 1 and 100")
	errInvalidBefore      = errors.New("before must be an RFC3339 timestamp")
	errInvalidContent     = errors.New("content is required and must be 1-10000 characters")
	errInvalidMessageType = errors.New(`message_type must be "text", "image", or "file"`)
)

// parseMessageLimit parses the ?limit= query param, defaulting to
// defaultMessageLimit when raw is empty.
func parseMessageLimit(raw string) (int, error) {
	if raw == "" {
		return defaultMessageLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maxMessageLimit {
		return 0, errInvalidLimit
	}
	return n, nil
}

// parseMessageBefore parses the ?before= query param; a nil result with a
// nil error means "not provided".
func parseMessageBefore(raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return nil, errInvalidBefore
	}
	return &t, nil
}

// validateMessageContent assumes content has already been trimmed.
func validateMessageContent(content string) error {
	if content == "" || len(content) > maxMessageContentLen {
		return errInvalidContent
	}
	return nil
}

// normalizeMessageType defaults raw to "text" when empty, and rejects
// anything other than the three client-sendable types — "system" included,
// since that's reserved for server-generated messages (see CLAUDE.md).
func normalizeMessageType(raw string) (models.MessageType, error) {
	messageType := models.MessageType(raw)
	if messageType == "" {
		messageType = models.MessageTypeText
	}
	switch messageType {
	case models.MessageTypeText, models.MessageTypeImage, models.MessageTypeFile:
		return messageType, nil
	default:
		return "", errInvalidMessageType
	}
}

// ListMessages handles GET /api/v1/rooms/{room_id}/messages?limit=&before=.
// before is an RFC3339 timestamp (the created_at of the oldest message from
// a previous page); omitting it returns the newest messages.
func ListMessages(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, _ := auth.UserIDFromContext(r.Context())

		roomID, _, ok := requireActiveMembership(w, r, s, userID)
		if !ok {
			return
		}

		limit, err := parseMessageLimit(r.URL.Query().Get("limit"))
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", err.Error())
			return
		}

		before, err := parseMessageBefore(r.URL.Query().Get("before"))
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", err.Error())
			return
		}

		messages, err := s.ListRoomMessages(r.Context(), roomID, limit, before)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to list messages")
			return
		}

		httpx.WriteJSON(w, http.StatusOK, messages)
	}
}

type sendMessageRequest struct {
	Content     string `json:"content"`
	MessageType string `json:"message_type,omitempty"`
}

// SendMessage handles POST /api/v1/rooms/{room_id}/messages — the REST
// fallback for sending when the WebSocket is unavailable. An optional
// Idempotency-Key header rejects a duplicate send within the dedup window.
func SendMessage(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, _ := auth.UserIDFromContext(r.Context())

		roomID, _, ok := requireActiveMembership(w, r, s, userID)
		if !ok {
			return
		}

		var req sendMessageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "request body must be valid JSON")
			return
		}

		content := strings.TrimSpace(req.Content)
		if err := validateMessageContent(content); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", err.Error())
			return
		}

		messageType, err := normalizeMessageType(req.MessageType)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", err.Error())
			return
		}

		key := r.Header.Get("Idempotency-Key")
		if key != "" {
			reserved, err := s.ReserveMessageIdempotencyKey(r.Context(), roomID, userID, key)
			if err != nil {
				httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to check idempotency key")
				return
			}
			if !reserved {
				httpx.WriteError(w, http.StatusConflict, "conflict", "duplicate request: idempotency key already used")
				return
			}
		}

		message, err := s.InsertMessage(r.Context(), roomID, userID, content, messageType)
		if err != nil {
			if key != "" {
				_ = s.ReleaseMessageIdempotencyKey(r.Context(), roomID, userID, key)
			}
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to send message")
			return
		}

		httpx.WriteJSON(w, http.StatusCreated, message)
	}
}

// DeleteMessage handles DELETE /api/v1/messages/{message_id}. Only the
// message's author or an admin of its room may delete it. This is always a
// soft delete (deleted_at) — the row is never removed, per the append-only
// schema — so a subsequent history fetch shows a masked placeholder rather
// than omitting the message.
func DeleteMessage(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, _ := auth.UserIDFromContext(r.Context())

		messageID, err := uuid.Parse(chi.URLParam(r, "message_id"))
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "message_id must be a valid UUID")
			return
		}

		message, err := s.GetMessageByID(r.Context(), messageID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				httpx.WriteError(w, http.StatusNotFound, "not_found", "message not found")
				return
			}
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to look up message")
			return
		}

		if message.UserID != userID {
			membership, err := s.GetMembership(r.Context(), message.RoomID, userID)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to check membership")
				return
			}
			isRoomAdmin := err == nil && membership.Role == models.RoleAdmin

			// A system admin can moderate any message in any room, including
			// ones they don't belong to — this is "message moderation" (see
			// plan.md's admin-dashboard proposal); it widens who may call this
			// existing endpoint rather than adding a separate one.
			if !isRoomAdmin {
				caller, err := s.GetUserByID(r.Context(), userID)
				if err != nil {
					httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to check admin status")
					return
				}
				if !caller.IsSystemAdmin {
					httpx.WriteError(w, http.StatusForbidden, "forbidden", "only the author, a room admin, or a system admin can delete this message")
					return
				}
			}
		}

		if err := s.SoftDeleteMessage(r.Context(), messageID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				httpx.WriteError(w, http.StatusNotFound, "not_found", "message not found")
				return
			}
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to delete message")
			return
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

type editMessageRequest struct {
	Content string `json:"content"`
}

// editMessageResponse is a minimal, purpose-built shape rather than the full
// MessageWithAuthor: content and edited_at are the only fields an edit
// changes, and the caller's already-cached copy already holds an accurate
// author/reactions/read_by — echoing those back here would mean either an
// extra batch-fetch to keep them honest or (worse) silently serving stale
// empty defaults for them, like a freshly-inserted message's response
// correctly does but an edited message's would not.
type editMessageResponse struct {
	ID       uuid.UUID `json:"id"`
	RoomID   uuid.UUID `json:"room_id"`
	Content  string    `json:"content"`
	EditedAt time.Time `json:"edited_at"`
}

// messageEditedEvent matches the WS "message.edited" shape: {"type",
// "id","room_id","content","edited_at"} — same minimal shape as the REST
// response above, and same publishing pattern as reactionEvent
// (internal/handlers/reactions.go): a REST handler calling
// store.PublishRoomEvent directly, no dependency on internal/websocket.
type messageEditedEvent struct {
	Type     string    `json:"type"`
	ID       uuid.UUID `json:"id"`
	RoomID   uuid.UUID `json:"room_id"`
	Content  string    `json:"content"`
	EditedAt time.Time `json:"edited_at"`
}

// EditMessage handles PATCH /api/v1/messages/{message_id}. Author-only — no
// admin override, unlike DeleteMessage: a room admin may remove disruptive
// content, but rewriting someone else's words is a different, more invasive
// power this app doesn't grant. Editing an already-deleted (or nonexistent)
// message 404s, matching DeleteMessage/ToggleReaction's "already gone → 404,
// not a silent no-op" idiom.
func EditMessage(s *store.Store, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, _ := auth.UserIDFromContext(r.Context())

		messageID, err := uuid.Parse(chi.URLParam(r, "message_id"))
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "message_id must be a valid UUID")
			return
		}

		var req editMessageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "request body must be valid JSON")
			return
		}
		content := strings.TrimSpace(req.Content)
		if err := validateMessageContent(content); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", err.Error())
			return
		}

		message, err := s.GetMessageByID(r.Context(), messageID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				httpx.WriteError(w, http.StatusNotFound, "not_found", "message not found")
				return
			}
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to look up message")
			return
		}
		if message.DeletedAt != nil {
			httpx.WriteError(w, http.StatusNotFound, "not_found", "message not found")
			return
		}
		if message.UserID != userID {
			httpx.WriteError(w, http.StatusForbidden, "forbidden", "only the author can edit this message")
			return
		}

		editedAt, err := s.EditMessage(r.Context(), messageID, content)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				httpx.WriteError(w, http.StatusNotFound, "not_found", "message not found")
				return
			}
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to edit message")
			return
		}

		// The edit is already persisted at this point, so a broadcast hiccup
		// shouldn't fail the request back to the caller — logged, not fatal,
		// same philosophy as ToggleReaction's publish step.
		payload, err := json.Marshal(messageEditedEvent{
			Type:     "message.edited",
			ID:       messageID,
			RoomID:   message.RoomID,
			Content:  content,
			EditedAt: editedAt,
		})
		if err != nil {
			logger.Error("marshaling message.edited event failed", "message_id", messageID, "error", err)
		} else if err := s.PublishRoomEvent(r.Context(), message.RoomID, payload); err != nil {
			logger.Warn("publishing message.edited event failed", "message_id", messageID, "room_id", message.RoomID, "error", err)
		}

		httpx.WriteJSON(w, http.StatusOK, editMessageResponse{
			ID:       messageID,
			RoomID:   message.RoomID,
			Content:  content,
			EditedAt: editedAt,
		})
	}
}
