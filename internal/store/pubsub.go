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

const userChannelPrefix = "user:"

func userChannel(userID uuid.UUID) string { return userChannelPrefix + userID.String() }

// PublishUserEvent publishes payload to a user's personal Pub/Sub channel
// (user:{id}) — for an event that a specific user must receive even though
// they've never joined any room's channel, e.g. being added to a brand-new
// room (see UserSubscription doc below for why a room channel alone can't
// deliver that).
func (s *Store) PublishUserEvent(ctx context.Context, userID uuid.UUID, payload []byte) error {
	if err := s.Redis.Publish(ctx, userChannel(userID), payload).Err(); err != nil {
		return fmt.Errorf("publishing user event: %w", err)
	}
	return nil
}

// UserMessage is one event received on a user's Pub/Sub channel, with the
// user ID decoded back out of the channel name.
type UserMessage struct {
	UserID  uuid.UUID
	Payload []byte
}

// UserSubscription is RoomSubscription's per-user counterpart: a dynamic
// Redis Pub/Sub subscription over user:{id} channels rather than room:{id}
// ones. It exists because the room-channel model has a chicken-and-egg gap —
// a user who's just been added to a room (invited, or a DM's peer) has never
// joined that room's WS channel, so a room:{id} broadcast can't reach them;
// their personal user:{id} channel, subscribed to for the life of every
// connection (see internal/websocket/server.go), can. Kept as a fully
// separate Redis Pub/Sub connection from RoomSubscription's — cheap, and it
// keeps this new delivery path from touching the already-verified room one.
type UserSubscription struct {
	pubsub *redis.PubSub
	done   chan struct{}
}

// NewUserSubscription opens a Pub/Sub connection subscribed to nothing yet;
// add users with Subscribe. Close releases the connection.
func (s *Store) NewUserSubscription(ctx context.Context) *UserSubscription {
	return &UserSubscription{
		pubsub: s.Redis.Subscribe(ctx),
		done:   make(chan struct{}),
	}
}

// Subscribe starts delivering events published to userID's channel. Safe to
// call concurrently with Messages.
func (us *UserSubscription) Subscribe(ctx context.Context, userID uuid.UUID) error {
	if err := us.pubsub.Subscribe(ctx, userChannel(userID)); err != nil {
		return fmt.Errorf("subscribing to user channel: %w", err)
	}
	return nil
}

// Unsubscribe stops delivering events for userID.
func (us *UserSubscription) Unsubscribe(ctx context.Context, userID uuid.UUID) error {
	if err := us.pubsub.Unsubscribe(ctx, userChannel(userID)); err != nil {
		return fmt.Errorf("unsubscribing from user channel: %w", err)
	}
	return nil
}

// Messages returns a channel of decoded user events. Mirrors
// RoomSubscription.Messages exactly — see its doc comment. Call exactly once
// per subscription.
func (us *UserSubscription) Messages() <-chan UserMessage {
	out := make(chan UserMessage)
	go func() {
		defer close(out)
		for msg := range us.pubsub.Channel() {
			userID, err := userIDFromChannel(msg.Channel)
			if err != nil {
				continue // ignore anything that isn't a user:{uuid} channel
			}
			select {
			case out <- UserMessage{UserID: userID, Payload: []byte(msg.Payload)}:
			case <-us.done: // Closed while a consumer wasn't reading; don't leak.
				return
			}
		}
	}()
	return out
}

// Close ends the subscription and unblocks the Messages goroutine.
func (us *UserSubscription) Close() error {
	close(us.done)
	return us.pubsub.Close()
}

func userIDFromChannel(channel string) (uuid.UUID, error) {
	idStr, ok := strings.CutPrefix(channel, userChannelPrefix)
	if !ok {
		return uuid.Nil, fmt.Errorf("channel %q missing %q prefix", channel, userChannelPrefix)
	}
	return uuid.Parse(idStr)
}
