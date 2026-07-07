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

	register   chan *Client
	unregister chan *Client
	join       chan roomSubscription
	leave      chan roomSubscription
	broadcast  chan Broadcast

	// done is closed when Run returns so that callers trying to hand the Hub a
	// command during shutdown unblock instead of leaking a goroutine.
	done chan struct{}

	// clients and rooms are owned exclusively by the Run goroutine. Never read
	// or write them from anywhere else.
	clients map[*Client]struct{}
	rooms   map[uuid.UUID]map[*Client]struct{}
}

type roomSubscription struct {
	client *Client
	roomID uuid.UUID
}

// Broadcast is an event destined for every client currently joined to RoomID,
// optionally skipping Except (the originating client, so a sender isn't echoed
// its own message). The delivery mechanism lives here so the Hub is complete;
// producers (message.new, user.joined/left, Redis fan-in) arrive with the
// message-routing step.
type Broadcast struct {
	RoomID  uuid.UUID
	Payload []byte
	Except  *Client
}

// NewHub constructs a Hub. Call Run (once) to start its owning goroutine.
func NewHub(logger *slog.Logger) *Hub {
	return &Hub{
		logger:     logger,
		register:   make(chan *Client, registerBuffer),
		unregister: make(chan *Client, registerBuffer),
		join:       make(chan roomSubscription, commandBuffer),
		leave:      make(chan roomSubscription, commandBuffer),
		broadcast:  make(chan Broadcast, commandBuffer),
		done:       make(chan struct{}),
		clients:    make(map[*Client]struct{}),
		rooms:      make(map[uuid.UUID]map[*Client]struct{}),
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
			h.logger.Info("ws client registered", "user_id", c.userID, "clients", len(h.clients))
		case c := <-h.unregister:
			h.removeClient(c)
		case sub := <-h.join:
			h.addToRoom(sub.client, sub.roomID)
		case sub := <-h.leave:
			h.removeFromRoom(sub.client, sub.roomID)
		case b := <-h.broadcast:
			h.deliver(b)
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
	for roomID := range c.rooms {
		h.detach(roomID, c)
	}
	close(c.send)
	h.logger.Info("ws client unregistered", "user_id", c.userID, "clients", len(h.clients))
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
	}
}

// deliver fans a broadcast out to a room's clients. A client whose send buffer
// is full is a slow consumer: it's dropped (which closes its send channel and
// unblocks its writePump) rather than allowed to stall the Hub. Deleting map
// entries mid-range — removeClient does, via detach — is safe in Go.
func (h *Hub) deliver(b Broadcast) {
	for c := range h.rooms[b.RoomID] {
		if c == b.Except {
			continue
		}
		select {
		case c.send <- b.Payload:
		default:
			h.logger.Warn("ws client dropped: send buffer full", "user_id", c.userID)
			h.removeClient(c)
		}
	}
}
