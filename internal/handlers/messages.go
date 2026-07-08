package handlers

import (
	"encoding/json"
	"errors"
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
			if err != nil || membership.Role != models.RoleAdmin {
				httpx.WriteError(w, http.StatusForbidden, "forbidden", "only the author or a room admin can delete this message")
				return
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
