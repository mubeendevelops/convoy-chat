package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/mubeendevelops/convoy-chat/internal/models"
	"github.com/mubeendevelops/convoy-chat/internal/store"
)

// maxMessageContentLen mirrors the REST send limit (handlers.SendMessage) so WS
// and REST accept identical content.
const maxMessageContentLen = 10000

// dispatch routes a single inbound frame by its "type". Handlers reply to the
// sending client with an error envelope on bad input, and fan out room events
// through Redis (never delivering locally themselves — see Broker).
func (s *Server) dispatch(c *Client, data []byte) {
	var env inboundEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		c.sendError("invalid_input", "message must be a JSON object with a type field")
		return
	}

	switch env.Type {
	case eventRoomJoin:
		s.handleRoomJoin(c, env)
	case eventRoomLeave:
		s.handleRoomLeave(c, env)
	case eventMessageSend:
		s.handleMessageSend(c, env)
	case "":
		c.sendError("invalid_input", "missing event type")
	default:
		c.sendError("unsupported", "event type not supported yet: "+env.Type)
	}
}

// handleRoomJoin gates on active membership (a client may only watch a room it
// belongs to — this is the authorization check deferred from the connection
// layer), starts tracking the client in the room, and announces user.joined.
func (s *Server) handleRoomJoin(c *Client, env inboundEnvelope) {
	roomID, err := uuid.Parse(env.RoomID)
	if err != nil {
		c.sendError("invalid_input", "room_id must be a valid UUID")
		return
	}

	ctx, cancel := context.WithTimeout(s.runCtx, dbTimeout)
	defer cancel()

	if _, err := s.store.GetMembership(ctx, roomID, c.userID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.sendError("forbidden", "not a member of this room")
			return
		}
		s.logger.Error("ws room.join membership check failed", "user_id", c.userID, "room_id", roomID, "error", err)
		c.sendError("internal_error", "failed to verify membership")
		return
	}

	s.hub.Join(c, roomID)

	s.publish(ctx, roomID, userJoinedEvent{
		Type:   eventUserJoined,
		User:   userRef{ID: c.userID, Username: c.username},
		RoomID: roomID,
	})
}

// handleRoomLeave stops tracking the client in the room and announces user.left.
// It doesn't gate on membership — a client may always stop watching a room.
func (s *Server) handleRoomLeave(c *Client, env inboundEnvelope) {
	roomID, err := uuid.Parse(env.RoomID)
	if err != nil {
		c.sendError("invalid_input", "room_id must be a valid UUID")
		return
	}

	s.hub.Leave(c, roomID)

	ctx, cancel := context.WithTimeout(s.runCtx, dbTimeout)
	defer cancel()
	s.publish(ctx, roomID, userLeftEvent{
		Type:   eventUserLeft,
		UserID: c.userID,
		RoomID: roomID,
	})
}

// handleMessageSend validates input, gates on active membership, persists the
// message, then fans out message.new. Validation mirrors handlers.SendMessage
// (the REST fallback) so both paths behave identically.
func (s *Server) handleMessageSend(c *Client, env inboundEnvelope) {
	roomID, err := uuid.Parse(env.RoomID)
	if err != nil {
		c.sendError("invalid_input", "room_id must be a valid UUID")
		return
	}

	content := strings.TrimSpace(env.Content)
	if content == "" || len(content) > maxMessageContentLen {
		c.sendError("invalid_input", "content is required and must be 1-10000 characters")
		return
	}

	messageType := models.MessageType(env.MessageType)
	if messageType == "" {
		messageType = models.MessageTypeText
	}
	switch messageType {
	case models.MessageTypeText, models.MessageTypeImage, models.MessageTypeFile:
	default:
		c.sendError("invalid_input", `message_type must be "text", "image", or "file"`)
		return
	}

	ctx, cancel := context.WithTimeout(s.runCtx, dbTimeout)
	defer cancel()

	if _, err := s.store.GetMembership(ctx, roomID, c.userID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.sendError("forbidden", "not a member of this room")
			return
		}
		s.logger.Error("ws message.send membership check failed", "user_id", c.userID, "room_id", roomID, "error", err)
		c.sendError("internal_error", "failed to verify membership")
		return
	}

	message, err := s.store.InsertMessage(ctx, roomID, c.userID, content, messageType)
	if err != nil {
		s.logger.Error("ws message.send persist failed", "user_id", c.userID, "room_id", roomID, "error", err)
		c.sendError("internal_error", "failed to send message")
		return
	}

	s.publish(ctx, roomID, messageNewEvent{
		Type: eventMessageNew,
		Message: messageNewPayload{
			ID:        message.ID,
			RoomID:    message.RoomID,
			User:      message.User,
			Content:   message.Content,
			CreatedAt: message.CreatedAt,
			ReadBy:    []uuid.UUID{},
		},
	})
}

// publish marshals event and publishes it to the room's Redis channel for
// delivery to every subscribed server. A publish failure is logged, not fatal:
// the message is already persisted and clients recover it via history/resync,
// so we don't fail the send back to the author.
func (s *Server) publish(ctx context.Context, roomID uuid.UUID, event any) {
	payload, err := json.Marshal(event)
	if err != nil {
		s.logger.Error("ws marshaling outbound event failed", "room_id", roomID, "error", err)
		return
	}
	if err := s.broker.Publish(ctx, roomID, payload); err != nil {
		s.logger.Warn("ws publishing room event failed", "room_id", roomID, "error", err)
	}
}
