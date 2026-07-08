package websocket

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"github.com/mubeendevelops/convoy-chat/internal/store"
)

const subRequestBuffer = 256

type subRequest struct {
	roomID    uuid.UUID
	subscribe bool // true = subscribe, false = unsubscribe

	// ready, when non-nil, receives the outcome of a subscribe request
	// (nil on success) and is then closed — see EnsureSubscribed. Never set
	// for unsubscribe requests, which are fire-and-forget.
	ready chan error
}

// Broker bridges the local Hub and Redis Pub/Sub for multi-server broadcast.
// Outbound room events are Published to room:{id}. Subscribing is driven by
// EnsureSubscribed (called from dispatch, before a room.join's publish —
// see its doc comment for why); unsubscribing is still driven by the Hub's
// last-local-client-leaves transition, since a slightly-delayed unsubscribe
// is harmless, unlike a slightly-delayed subscribe. Every received event is
// handed to the Hub for local delivery; delivery happens ONLY on this
// receive path (never at publish time), so the origin server delivers its
// own events exactly once when they come back — no double-delivery.
type Broker struct {
	store  *store.Store
	hub    *Hub
	logger *slog.Logger

	// subReqs carries subscribe/unsubscribe requests to Run, keeping Redis
	// I/O off both the Hub goroutine and whichever goroutine is asking.
	subReqs chan subRequest

	// sub and subscribed are owned by the Run goroutine only. subscribed
	// tracks rooms with a confirmed-active subscription so a redundant
	// EnsureSubscribed call (the common case — most joins aren't the room's
	// first local client) resolves instantly, with no Redis round trip.
	sub        *store.RoomSubscription
	subscribed map[uuid.UUID]struct{}
}

func NewBroker(st *store.Store, hub *Hub, logger *slog.Logger) *Broker {
	return &Broker{
		store:      st,
		hub:        hub,
		logger:     logger,
		subReqs:    make(chan subRequest, subRequestBuffer),
		subscribed: make(map[uuid.UUID]struct{}),
	}
}

// EnsureSubscribed blocks the calling goroutine (never the Hub's, and never
// Run's own) until this server has a confirmed-active Redis subscription for
// roomID — subscribing now if it doesn't already have one, or returning
// immediately if it does.
//
// Callers about to publish to a room they just (or already) have local
// interest in must call this first: PUBLISH doesn't queue for a
// not-yet-subscribed channel, it's simply discarded, so publishing before
// the SUBSCRIBE has actually landed silently drops the event. This isn't
// theoretical — a bare "Join then publish" (the original Phase 5 shape)
// measured a ~97% loss rate for a joiner's own user.joined in Phase 8's
// integration tests, because the old Subscribe path had two extra
// asynchronous goroutine hops (dispatch → Hub → Broker.Run) behind an
// immediate publish on the same calling goroutine.
func (b *Broker) EnsureSubscribed(ctx context.Context, roomID uuid.UUID) error {
	ready := make(chan error, 1)
	select {
	case b.subReqs <- subRequest{roomID: roomID, subscribe: true, ready: ready}:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-ready:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Unsubscribe implements roomSubscriber. The Hub calls it from its own
// goroutine when a room's last local client leaves, so it only enqueues the
// request (fire-and-forget) — the actual Redis call happens in Run.
func (b *Broker) Unsubscribe(roomID uuid.UUID) {
	select {
	case b.subReqs <- subRequest{roomID: roomID, subscribe: false}:
	default:
		// Best-effort: a dropped unsubscribe just means this server keeps
		// receiving a room's events a little longer than necessary (wasted
		// work, not a correctness problem — unlike a dropped subscribe).
		// The buffer is large and requests are rare, so this should never
		// fire in practice.
		b.logger.Error("ws broker backlog full: unsubscribe request dropped", "room_id", roomID)
	}
}

// Publish sends a room event to every subscribed server (this one included).
// Callers run in per-connection goroutines, so blocking on Redis here is fine.
func (b *Broker) Publish(ctx context.Context, roomID uuid.UUID, payload []byte) error {
	return b.store.PublishRoomEvent(ctx, roomID, payload)
}

// Run owns the Redis subscription for this server's lifetime: it applies
// subscribe/unsubscribe requests and forwards received events to the Hub. It
// returns when ctx is cancelled (graceful shutdown).
func (b *Broker) Run(ctx context.Context) {
	b.sub = b.store.NewRoomSubscription(ctx)
	defer func() { _ = b.sub.Close() }()

	messages := b.sub.Messages()

	for {
		select {
		case <-ctx.Done():
			return
		case req := <-b.subReqs:
			b.applySubRequest(ctx, req)
		case msg, ok := <-messages:
			if !ok {
				return // subscription closed
			}
			b.hub.Broadcast(Broadcast{RoomID: msg.RoomID, Payload: msg.Payload})
		}
	}
}

func (b *Broker) applySubRequest(ctx context.Context, req subRequest) {
	if !req.subscribe {
		if err := b.sub.Unsubscribe(ctx, req.roomID); err != nil {
			b.logger.Warn("ws redis subscription change failed", "room_id", req.roomID, "subscribe", false, "error", err)
		}
		delete(b.subscribed, req.roomID)
		return
	}

	var err error
	if _, already := b.subscribed[req.roomID]; !already {
		if err = b.sub.Subscribe(ctx, req.roomID); err == nil {
			b.subscribed[req.roomID] = struct{}{}
		} else {
			b.logger.Warn("ws redis subscription change failed", "room_id", req.roomID, "subscribe", true, "error", err)
		}
	}
	if req.ready != nil {
		req.ready <- err
		close(req.ready)
	}
}
