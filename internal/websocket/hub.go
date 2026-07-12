// Package websocket implements the real-time layer. A single goroutine (the
// Hub) owns all shared connection state — the set of connected clients and the
// roomID → clients index — and every mutation happens inside that goroutine,
// driven over channels (register / unregister / join / leave / broadcast).
// There are deliberately no mutexes: state is confined to one goroutine, so
// there are no locks to reason about (see CLAUDE.md "Hub concurrency"). The
// per-connection read/write pumps in client.go talk to the Hub exclusively
// through these channels.
package websocket

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
)

// Channel buffer depths. Generous enough that ordinary register/join churn
// never blocks a caller; the send-side backpressure that actually matters is
// per-client (see deliver), not on these command channels.
const (
	registerBuffer = 64
	commandBuffer  = 256
)

// Hub is the connection manager. Exactly one goroutine — Run — reads these
// channels and mutates clients and rooms; nothing else touches that state.
type Hub struct {
	logger *slog.Logger

	register      chan *Client
	unregister    chan *Client
	join          chan roomSubscription
	leave         chan roomSubscription
	broadcast     chan Broadcast
	userBroadcast chan UserBroadcast

	// done is closed when Run returns so that callers trying to hand the Hub a
	// command during shutdown unblock instead of leaking a goroutine.
	done chan struct{}

	// subscriber (optional) is told when a room gains its first / loses its last
	// local client, so Redis subscriptions track local interest. Set once before
	// Run via SetSubscriber; read only from the Run goroutine thereafter.
	subscriber roomSubscriber

	// presence (optional) is told when a client disconnects, along with the
	// rooms it had joined, so presence state and room-departure events can be
	// updated off the Hub goroutine. Set once before Run via
	// SetPresenceNotifier; read only from the Run goroutine thereafter.
	presence presenceNotifier

	// userSubscriber (optional) is told when a user's last local connection
	// disconnects, so its Redis user:{id} subscription tracks local interest,
	// the same way subscriber does for rooms. Set once before Run via
	// SetUserSubscriber; read only from the Run goroutine thereafter.
	userSubscriber userSubscriber

	// clients and rooms are owned exclusively by the Run goroutine. Never read
	// or write them from anywhere else.
	clients map[*Client]struct{}
	rooms   map[uuid.UUID]map[*Client]struct{}

	// users indexes locally-connected clients by user_id, independent of room
	// membership — every registered client sits in its own userID's set for
	// the life of the connection (no explicit join/leave, unlike rooms). This
	// is what lets UserBroadcast reach a user who has never joined any room's
	// channel yet — see UserBroadcast and store.UserSubscription.
	users map[uuid.UUID]map[*Client]struct{}
}

type roomSubscription struct {
	client *Client
	roomID uuid.UUID
}

// Broadcast is an event to deliver to every client currently joined to RoomID
// on this server. It's fed by the Broker's Redis subscription (never directly
// by a send handler), so delivery happens on exactly one path — the origin
// server receives its own published events back and delivers them here once,
// which is why there's no double-delivery and no per-client exclusion.
type Broadcast struct {
	RoomID  uuid.UUID
	Payload []byte
}

// UserBroadcast is Broadcast's per-user counterpart: an event to deliver to
// every local connection belonging to UserID, regardless of which rooms (if
// any) those connections have joined. Also fed only by the Broker's Redis
// subscription, for the same single-delivery-path reason as Broadcast.
type UserBroadcast struct {
	UserID  uuid.UUID
	Payload []byte
}

// roomSubscriber is notified when this server loses a room's last local
// client, so it can drop the matching Redis subscription. Subscribing is
// deliberately NOT symmetric — see Broker.EnsureSubscribed — a slightly-late
// unsubscribe just means extra, harmless local delivery attempts to an
// empty room, unlike a slightly-late subscribe, which loses events outright.
// Implemented by *Broker; nil in tests or single-node setups without Redis.
type roomSubscriber interface {
	Unsubscribe(roomID uuid.UUID)
}

// userSubscriber is notified when this server loses a user's last local
// connection, so it can drop the matching Redis user:{id} subscription.
// Mirrors roomSubscriber exactly, one level up (per-user instead of
// per-room). Implemented by *Broker; nil in tests or single-node setups
// without Redis.
type userSubscriber interface {
	UnsubscribeUser(userID uuid.UUID)
}

// presenceNotifier is told when a client disconnects, along with the rooms it
// had joined, so presence/room-departure bookkeeping (Redis + Postgres + a
// Redis Pub/Sub publish) can happen off the Hub goroutine. Implemented by
// *Server; nil in tests that don't need presence.
type presenceNotifier interface {
	ClientDisconnected(userID uuid.UUID, rooms []uuid.UUID)
}

// SetSubscriber wires the Redis room subscriber. Call once, before Run.
func (h *Hub) SetSubscriber(s roomSubscriber) { h.subscriber = s }

// SetPresenceNotifier wires presence/room-departure handling. Call once, before Run.
func (h *Hub) SetPresenceNotifier(p presenceNotifier) { h.presence = p }

// SetUserSubscriber wires the Redis per-user subscriber. Call once, before Run.
func (h *Hub) SetUserSubscriber(s userSubscriber) { h.userSubscriber = s }

// NewHub constructs a Hub. Call Run (once) to start its owning goroutine.
func NewHub(logger *slog.Logger) *Hub {
	return &Hub{
		logger:        logger,
		register:      make(chan *Client, registerBuffer),
		unregister:    make(chan *Client, registerBuffer),
		join:          make(chan roomSubscription, commandBuffer),
		leave:         make(chan roomSubscription, commandBuffer),
		broadcast:     make(chan Broadcast, commandBuffer),
		userBroadcast: make(chan UserBroadcast, commandBuffer),
		done:          make(chan struct{}),
		clients:       make(map[*Client]struct{}),
		rooms:         make(map[uuid.UUID]map[*Client]struct{}),
		users:         make(map[uuid.UUID]map[*Client]struct{}),
	}
}

// Run is the Hub's single owning goroutine. It returns when ctx is cancelled
// (graceful shutdown); in-flight connections are then torn down by their own
// pumps as the process exits.
func (h *Hub) Run(ctx context.Context) {
	defer close(h.done)
	for {
		select {
		case <-ctx.Done():
			return
		case c := <-h.register:
			h.clients[c] = struct{}{}
			users := h.users[c.userID]
			if users == nil {
				users = make(map[*Client]struct{})
				h.users[c.userID] = users
			}
			users[c] = struct{}{}
			h.logger.Info("ws client registered", "user_id", c.userID, "clients", len(h.clients))
		case c := <-h.unregister:
			h.removeClient(c)
		case sub := <-h.join:
			h.addToRoom(sub.client, sub.roomID)
		case sub := <-h.leave:
			h.removeFromRoom(sub.client, sub.roomID)
		case b := <-h.broadcast:
			h.deliver(b)
		case b := <-h.userBroadcast:
			h.deliverToUser(b)
		}
	}
}

// Register enqueues a newly connected client. Called once per connection by the
// upgrade handler before the pumps start.
func (h *Hub) Register(c *Client) { h.enqueueClient(h.register, c) }

// Unregister enqueues a client for teardown. Called from readPump's defer — the
// single place a disconnect is initiated.
func (h *Hub) Unregister(c *Client) { h.enqueueClient(h.unregister, c) }

// Join / Leave enqueue a room membership change for c.
func (h *Hub) Join(c *Client, roomID uuid.UUID)  { h.enqueueSub(h.join, c, roomID) }
func (h *Hub) Leave(c *Client, roomID uuid.UUID) { h.enqueueSub(h.leave, c, roomID) }

// Broadcast enqueues an event for delivery to a room's local clients. Called by
// the Broker's Redis subscription loop — the only producer of broadcasts.
func (h *Hub) Broadcast(b Broadcast) {
	select {
	case h.broadcast <- b:
	case <-h.done:
	}
}

// BroadcastToUser enqueues an event for delivery to a user's local
// connections. Called by the Broker's Redis subscription loop, same as
// Broadcast — the only producer.
func (h *Hub) BroadcastToUser(b UserBroadcast) {
	select {
	case h.userBroadcast <- b:
	case <-h.done:
	}
}

func (h *Hub) enqueueClient(ch chan *Client, c *Client) {
	select {
	case ch <- c:
	case <-h.done: // Hub stopped (shutdown); drop the command rather than block.
	}
}

func (h *Hub) enqueueSub(ch chan roomSubscription, c *Client, roomID uuid.UUID) {
	select {
	case ch <- roomSubscription{client: c, roomID: roomID}:
	case <-h.done:
	}
}

// --- everything below runs only inside the Run goroutine ---

// removeClient tears a client out of all Hub state and closes its send channel,
// which signals writePump to flush a close frame and exit. The membership guard
// makes a second unregister (e.g. readPump erroring after the Hub already
// dropped a slow client) a no-op instead of a double close.
func (h *Hub) removeClient(c *Client) {
	if _, ok := h.clients[c]; !ok {
		return
	}
	delete(h.clients, c)
	rooms := make([]uuid.UUID, 0, len(c.rooms))
	for roomID := range c.rooms {
		rooms = append(rooms, roomID)
		h.detach(roomID, c)
	}
	h.detachUser(c)
	close(c.send)
	if h.presence != nil {
		h.presence.ClientDisconnected(c.userID, rooms)
	}
	h.logger.Info("ws client unregistered", "user_id", c.userID, "clients", len(h.clients))
}

// detachUser removes c from the per-user index, dropping the user's entry
// entirely (and its Redis subscription, via userSubscriber) once their last
// local connection is gone — mirrors detach's room-side logic exactly.
func (h *Hub) detachUser(c *Client) {
	users := h.users[c.userID]
	if users == nil {
		return
	}
	delete(users, c)
	if len(users) == 0 {
		delete(h.users, c.userID)
		if h.userSubscriber != nil {
			h.userSubscriber.UnsubscribeUser(c.userID)
		}
	}
}

func (h *Hub) addToRoom(c *Client, roomID uuid.UUID) {
	if _, ok := h.clients[c]; !ok {
		return // client disconnected before its join was processed
	}
	if _, ok := c.rooms[roomID]; ok {
		return // already joined — idempotent
	}
	members := h.rooms[roomID]
	if members == nil {
		members = make(map[*Client]struct{})
		h.rooms[roomID] = members
	}
	members[c] = struct{}{}
	c.rooms[roomID] = struct{}{}
	h.logger.Info("ws room join", "user_id", c.userID, "room_id", roomID, "room_size", len(members))
}

func (h *Hub) removeFromRoom(c *Client, roomID uuid.UUID) {
	if _, ok := c.rooms[roomID]; !ok {
		return // not in the room — idempotent
	}
	delete(c.rooms, roomID)
	h.detach(roomID, c)
	h.logger.Info("ws room leave", "user_id", c.userID, "room_id", roomID, "room_size", len(h.rooms[roomID]))
}

// detach removes c from the room index only (the caller manages c.rooms). It
// drops a room entry entirely once empty so the index can't grow unbounded.
func (h *Hub) detach(roomID uuid.UUID, c *Client) {
	members := h.rooms[roomID]
	if members == nil {
		return
	}
	delete(members, c)
	if len(members) == 0 {
		delete(h.rooms, roomID)
		if h.subscriber != nil {
			// Last local client left → stop receiving this room's Redis fan-out.
			h.subscriber.Unsubscribe(roomID)
		}
	}
}

// deliver fans a broadcast out to a room's clients. A client whose send buffer
// is full is a slow consumer: it's dropped (which closes its send channel and
// unblocks its writePump) rather than allowed to stall the Hub. Deleting map
// entries mid-range — removeClient does, via detach — is safe in Go.
func (h *Hub) deliver(b Broadcast) {
	for c := range h.rooms[b.RoomID] {
		select {
		case c.send <- b.Payload:
		default:
			h.logger.Warn("ws client dropped: send buffer full", "user_id", c.userID)
			h.removeClient(c)
		}
	}
}

// deliverToUser fans a UserBroadcast out to a user's local connections.
// Mirrors deliver exactly, indexed by user_id instead of room_id.
func (h *Hub) deliverToUser(b UserBroadcast) {
	for c := range h.users[b.UserID] {
		select {
		case c.send <- b.Payload:
		default:
			h.logger.Warn("ws client dropped: send buffer full", "user_id", c.userID)
			h.removeClient(c)
		}
	}
}
