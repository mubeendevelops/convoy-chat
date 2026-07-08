package websocket

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/mubeendevelops/convoy-chat/internal/models"
)

const (
	// presenceHeartbeatInterval is how often a live connection refreshes its
	// presence TTL (see client.go's writePump). presenceTTL is 2x that, so a
	// single missed beat doesn't flip a live connection offline.
	presenceHeartbeatInterval = 15 * time.Second
	presenceTTL               = 30 * time.Second

	// typingTimeout is how long a typing indicator survives with no follow-up
	// typing.start or explicit typing.stop before the server clears it itself.
	typingTimeout = 5 * time.Second

	disconnectBuffer = 256
)

// presenceDisconnectEvent carries what's needed to fully tear down a
// connection's presence/room-membership footprint. It's built on the Hub
// goroutine (removeClient has c.rooms right there) but processed off it,
// since applying it takes Redis/Postgres I/O — same reasoning as
// roomSubscriber.
type presenceDisconnectEvent struct {
	userID uuid.UUID
	rooms  []uuid.UUID
}

// ClientDisconnected implements presenceNotifier. Called synchronously from
// inside the Hub goroutine (removeClient), so — like Broker.enqueue — it must
// never block: a full backlog is logged and the event dropped rather than
// stalling every other client's register/join/leave/broadcast.
func (s *Server) ClientDisconnected(userID uuid.UUID, rooms []uuid.UUID) {
	select {
	case s.disconnects <- presenceDisconnectEvent{userID: userID, rooms: rooms}:
	default:
		s.logger.Error("ws presence backlog full: disconnect event dropped", "user_id", userID)
	}
}

// processDisconnects drains disconnect events and applies their (potentially
// slow) side effects: announcing user.left for every room the connection had
// joined, then — if this was the user's last live connection anywhere —
// flipping presence to offline. One goroutine is plenty; disconnects are far
// rarer than message sends, and this keeps them simply ordered rather than
// parallelized.
func (s *Server) processDisconnects(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-s.disconnects:
			dctx, cancel := context.WithTimeout(ctx, dbTimeout)
			s.presenceOffline(dctx, ev.userID, ev.rooms)
			cancel()
		}
	}
}

// presenceOnline records a new connection and, only on the user's first
// connection across every server instance, marks them online and announces
// it to every room they belong to. Called asynchronously right after
// Hub.Register (see Handler) so it never delays the handshake or the pumps
// starting.
func (s *Server) presenceOnline(ctx context.Context, userID uuid.UUID) {
	wentOnline, err := s.store.PresenceConnect(ctx, userID, presenceTTL)
	if err != nil {
		s.logger.Error("presence connect failed", "user_id", userID, "error", err)
		return
	}
	if wentOnline {
		s.broadcastStatusChanged(ctx, userID, models.PresenceOnline)
	}
}

// presenceOffline announces user.left for every room the disconnecting
// connection had joined — closing the Phase 5 deferral, so a dropped
// connection (not just an explicit room.leave) now emits it — then, only if
// this was the user's last live connection anywhere, flips presence to
// offline and announces that too.
func (s *Server) presenceOffline(ctx context.Context, userID uuid.UUID, rooms []uuid.UUID) {
	for _, roomID := range rooms {
		s.publish(ctx, roomID, userLeftEvent{Type: eventUserLeft, UserID: userID, RoomID: roomID})
	}

	wentOffline, err := s.store.PresenceDisconnect(ctx, userID)
	if err != nil {
		s.logger.Error("presence disconnect failed", "user_id", userID, "error", err)
		return
	}
	if wentOffline {
		s.broadcastStatusChanged(ctx, userID, models.PresenceOffline)
	}
}

// presenceHeartbeat refreshes a live connection's presence TTL. Called every
// presenceHeartbeatInterval from writePump, which already isn't the Hub
// goroutine, so the direct Redis round trip is fine.
func (s *Server) presenceHeartbeat(ctx context.Context, userID uuid.UUID) {
	if err := s.store.PresenceHeartbeat(ctx, userID, presenceTTL); err != nil {
		s.logger.Warn("presence heartbeat failed", "user_id", userID, "error", err)
	}
}

// presenceUpdate applies an explicit online/away/offline change and
// announces it to every room the caller belongs to.
func (s *Server) presenceUpdate(ctx context.Context, userID uuid.UUID, status models.PresenceStatus) {
	if err := s.store.PresenceSetStatus(ctx, userID, status, presenceTTL); err != nil {
		s.logger.Error("presence update failed", "user_id", userID, "status", status, "error", err)
		return
	}
	s.broadcastStatusChanged(ctx, userID, status)
}

// broadcastStatusChanged fans user.status_changed out to every room userID
// currently belongs to — presence is account-wide, not scoped to whichever
// room(s) they happen to have joined over WS.
func (s *Server) broadcastStatusChanged(ctx context.Context, userID uuid.UUID, status models.PresenceStatus) {
	rooms, err := s.store.ListRoomsForUser(ctx, userID)
	if err != nil {
		s.logger.Error("listing rooms for presence broadcast failed", "user_id", userID, "error", err)
		return
	}
	event := userStatusChangedEvent{
		Type:       eventUserStatusChanged,
		UserID:     userID,
		Status:     status,
		LastSeenAt: time.Now().UTC(),
	}
	for _, room := range rooms {
		s.publish(ctx, room.ID, event)
	}
}
