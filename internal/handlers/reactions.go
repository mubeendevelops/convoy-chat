package handlers

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mubeendevelops/convoy-chat/internal/auth"
	"github.com/mubeendevelops/convoy-chat/internal/httpx"
	"github.com/mubeendevelops/convoy-chat/internal/store"
)

const maxEmojiLen = 10

type reactionRequest struct {
	Emoji string `json:"emoji"`
}

// reactionEvent matches the WS "message.reaction" shape from CLAUDE.md:
// {"type":"message.reaction","message_id","user_id","emoji","action"}.
// Defined here rather than in internal/websocket because publishing only
// needs store.PublishRoomEvent, not the Hub/Broker — REST handlers have
// never depended on the websocket package, and this doesn't need to start.
type reactionEvent struct {
	Type      string    `json:"type"`
	MessageID uuid.UUID `json:"message_id"`
	UserID    uuid.UUID `json:"user_id"`
	Emoji     string    `json:"emoji"`
	Action    string    `json:"action"`
}

// ToggleReaction handles POST /api/v1/messages/{message_id}/reactions.
// Reacting with an emoji the caller hasn't used yet on this message adds it;
// reacting with one they've already used removes it — one endpoint either
// way, so the client doesn't need to track prior state to pick a verb.
// Member-only (checked via the message's room); 404s for a nonexistent or
// already soft-deleted message, matching DeleteMessage's precedent.
func ToggleReaction(s *store.Store, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, _ := auth.UserIDFromContext(r.Context())

		messageID, err := uuid.Parse(chi.URLParam(r, "message_id"))
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "message_id must be a valid UUID")
			return
		}

		var req reactionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "request body must be valid JSON")
			return
		}
		emoji := strings.TrimSpace(req.Emoji)
		if emoji == "" || len(emoji) > maxEmojiLen {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "emoji is required and must be at most 10 characters")
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

		if _, err := s.GetMembership(r.Context(), message.RoomID, userID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				httpx.WriteError(w, http.StatusForbidden, "forbidden", "you are not a member of this room")
				return
			}
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to check membership")
			return
		}

		added, err := s.ToggleReaction(r.Context(), messageID, userID, emoji)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to toggle reaction")
			return
		}

		action := "removed"
		status := http.StatusOK
		if added {
			action = "added"
			status = http.StatusCreated
		}

		// The reaction is already persisted at this point, so a broadcast
		// hiccup shouldn't fail the request back to the caller — logged, not
		// fatal, same philosophy as the WS layer's own publish() helper.
		payload, err := json.Marshal(reactionEvent{
			Type:      "message.reaction",
			MessageID: messageID,
			UserID:    userID,
			Emoji:     emoji,
			Action:    action,
		})
		if err != nil {
			logger.Error("marshaling reaction event failed", "message_id", messageID, "error", err)
		} else if err := s.PublishRoomEvent(r.Context(), message.RoomID, payload); err != nil {
			logger.Warn("publishing reaction event failed", "message_id", messageID, "room_id", message.RoomID, "error", err)
		}

		httpx.WriteJSON(w, status, map[string]string{"status": action, "emoji": emoji})
	}
}
