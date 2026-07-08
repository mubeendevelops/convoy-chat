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
}

// Broker bridges the local Hub and Redis Pub/Sub for multi-server broadcast.
// Outbound room events are Published to room:{id}; the Broker keeps a Redis
// subscription tracking the rooms this server has local clients in, and hands
// every received event to the Hub for local delivery. Delivery happens ONLY on
// this receive path (never at publish time), so the origin server delivers its
// own events exactly once when they come back — no double-delivery.
type Broker struct {
	store  *store.Store
	hub    *Hub
	logger *slog.Logger

	// subReqs carries subscribe/unsubscribe requests from the Hub goroutine to
	// Run, keeping Redis I/O off the Hub goroutine.
	subReqs chan subRequest

	// sub is owned by the Run goroutine only.
	sub *store.RoomSubscription
}

func NewBroker(st *store.Store, hub *Hub, logger *slog.Logger) *Broker {
	return &Broker{
		store:   st,
		hub:     hub,
		logger:  logger,
		subReqs: make(chan subRequest, subRequestBuffer),
	}
}

// Subscribe / Unsubscribe implement roomSubscriber. The Hub calls them from its
// own goroutine on a room's first local join / last local leave, so they only
// enqueue the request — the actual Redis call happens in Run.
func (b *Broker) Subscribe(roomID uuid.UUID)   { b.enqueue(roomID, true) }
func (b *Broker) Unsubscribe(roomID uuid.UUID) { b.enqueue(roomID, false) }

func (b *Broker) enqueue(roomID uuid.UUID, subscribe bool) {
	select {
	case b.subReqs <- subRequest{roomID: roomID, subscribe: subscribe}:
	default:
		// A dropped subscribe would silently miss a room's events, so surface it
		// loudly. The buffer is large and requests are rare (one per 0↔1 room
		// transition), so this should never fire in practice.
		b.logger.Error("ws broker backlog full: subscription request dropped", "room_id", roomID, "subscribe", subscribe)
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
	defer b.sub.Close()

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
	var err error
	if req.subscribe {
		err = b.sub.Subscribe(ctx, req.roomID)
	} else {
		err = b.sub.Unsubscribe(ctx, req.roomID)
	}
	if err != nil {
		b.logger.Warn("ws redis subscription change failed", "room_id", req.roomID, "subscribe", req.subscribe, "error", err)
	}
}
