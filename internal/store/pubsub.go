package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const roomChannelPrefix = "room:"

func roomChannel(roomID uuid.UUID) string { return roomChannelPrefix + roomID.String() }

// PublishRoomEvent publishes payload to a room's Pub/Sub channel (room:{id}) so
// that every server currently subscribed to that room delivers it to its local
// WebSocket clients. This is the fan-out side of multi-server broadcast.
func (s *Store) PublishRoomEvent(ctx context.Context, roomID uuid.UUID, payload []byte) error {
	if err := s.Redis.Publish(ctx, roomChannel(roomID), payload).Err(); err != nil {
		return fmt.Errorf("publishing room event: %w", err)
	}
	return nil
}

// RoomMessage is one event received on a room's Pub/Sub channel, with the room
// decoded back out of the channel name.
type RoomMessage struct {
	RoomID  uuid.UUID
	Payload []byte
}

// RoomSubscription is a dynamic Redis Pub/Sub subscription over room channels:
// Subscribe/Unsubscribe adjust the set of rooms and Messages streams decoded
// events. All go-redis Pub/Sub access stays behind this type so the rest of the
// app never touches the client directly (per the store conventions).
type RoomSubscription struct {
	pubsub *redis.PubSub
	done   chan struct{}
}

// NewRoomSubscription opens a Pub/Sub connection subscribed to nothing yet; add
// rooms with Subscribe. Close releases the connection.
func (s *Store) NewRoomSubscription(ctx context.Context) *RoomSubscription {
	return &RoomSubscription{
		pubsub: s.Redis.Subscribe(ctx),
		done:   make(chan struct{}),
	}
}

// Subscribe starts delivering events published to roomID's channel. Safe to
// call concurrently with Messages.
func (rs *RoomSubscription) Subscribe(ctx context.Context, roomID uuid.UUID) error {
	if err := rs.pubsub.Subscribe(ctx, roomChannel(roomID)); err != nil {
		return fmt.Errorf("subscribing to room channel: %w", err)
	}
	return nil
}

// Unsubscribe stops delivering events for roomID.
func (rs *RoomSubscription) Unsubscribe(ctx context.Context, roomID uuid.UUID) error {
	if err := rs.pubsub.Unsubscribe(ctx, roomChannel(roomID)); err != nil {
		return fmt.Errorf("unsubscribing from room channel: %w", err)
	}
	return nil
}

// Messages returns a channel of decoded room events. It runs one goroutine that
// reads the underlying Pub/Sub stream and parses each channel name back to a
// room ID; the returned channel closes when the subscription is Closed. Call
// exactly once per subscription.
func (rs *RoomSubscription) Messages() <-chan RoomMessage {
	out := make(chan RoomMessage)
	go func() {
		defer close(out)
		for msg := range rs.pubsub.Channel() {
			roomID, err := roomIDFromChannel(msg.Channel)
			if err != nil {
				continue // ignore anything that isn't a room:{uuid} channel
			}
			select {
			case out <- RoomMessage{RoomID: roomID, Payload: []byte(msg.Payload)}:
			case <-rs.done: // Closed while a consumer wasn't reading; don't leak.
				return
			}
		}
	}()
	return out
}

// Close ends the subscription and unblocks the Messages goroutine.
func (rs *RoomSubscription) Close() error {
	close(rs.done)
	return rs.pubsub.Close()
}

func roomIDFromChannel(channel string) (uuid.UUID, error) {
	idStr, ok := strings.CutPrefix(channel, roomChannelPrefix)
	if !ok {
		return uuid.Nil, fmt.Errorf("channel %q missing %q prefix", channel, roomChannelPrefix)
	}
	return uuid.Parse(idStr)
}
