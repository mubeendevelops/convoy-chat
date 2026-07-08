package store_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mubeendevelops/convoy-chat/internal/store"
	"github.com/mubeendevelops/convoy-chat/internal/testutil"
)

const pubsubWait = 3 * time.Second

func expectMessage(t *testing.T, ch <-chan store.RoomMessage, want store.RoomMessage) {
	t.Helper()
	select {
	case got := <-ch:
		if got.RoomID != want.RoomID {
			t.Errorf("got room %s, want %s", got.RoomID, want.RoomID)
		}
		if string(got.Payload) != string(want.Payload) {
			t.Errorf("got payload %q, want %q", got.Payload, want.Payload)
		}
	case <-time.After(pubsubWait):
		t.Fatalf("timed out after %s waiting for a message on room %s", pubsubWait, want.RoomID)
	}
}

func expectNoMessage(t *testing.T, ch <-chan store.RoomMessage, within time.Duration) {
	t.Helper()
	select {
	case got := <-ch:
		t.Errorf("expected no message within %s, got one for room %s: %s", within, got.RoomID, got.Payload)
	case <-time.After(within):
	}
}

// TestRoomSubscription_CrossSubscriberDelivery proves the core guarantee the
// whole multi-server broadcast design (Phase 5) depends on: a message
// published on one connection reaches every independent subscriber watching
// that room, on other connections — not just the one that published it.
func TestRoomSubscription_CrossSubscriberDelivery(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	roomA := uuid.New()

	// Two independent subscriptions, simulating two separate server
	// instances each running their own Broker.
	sub1 := s.NewRoomSubscription(ctx)
	defer func() { _ = sub1.Close() }()
	sub2 := s.NewRoomSubscription(ctx)
	defer func() { _ = sub2.Close() }()

	if err := sub1.Subscribe(ctx, roomA); err != nil {
		t.Fatalf("sub1.Subscribe: %v", err)
	}
	if err := sub2.Subscribe(ctx, roomA); err != nil {
		t.Fatalf("sub2.Subscribe: %v", err)
	}
	ch1, ch2 := sub1.Messages(), sub2.Messages()

	payload := []byte(`{"type":"message.new","content":"hello"}`)
	if err := s.PublishRoomEvent(ctx, roomA, payload); err != nil {
		t.Fatalf("PublishRoomEvent: %v", err)
	}

	want := store.RoomMessage{RoomID: roomA, Payload: payload}
	expectMessage(t, ch1, want)
	expectMessage(t, ch2, want)
}

// TestRoomSubscription_RoomIsolation confirms a subscriber watching room A
// never sees events published to room B, even when both rooms have active
// subscribers concurrently.
func TestRoomSubscription_RoomIsolation(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	roomA := uuid.New()
	roomB := uuid.New()

	subA := s.NewRoomSubscription(ctx)
	defer func() { _ = subA.Close() }()
	subB := s.NewRoomSubscription(ctx)
	defer func() { _ = subB.Close() }()

	if err := subA.Subscribe(ctx, roomA); err != nil {
		t.Fatalf("subA.Subscribe(roomA): %v", err)
	}
	if err := subB.Subscribe(ctx, roomB); err != nil {
		t.Fatalf("subB.Subscribe(roomB): %v", err)
	}
	chA, chB := subA.Messages(), subB.Messages()

	payload := []byte(`{"type":"message.new","content":"only for room A"}`)
	if err := s.PublishRoomEvent(ctx, roomA, payload); err != nil {
		t.Fatalf("PublishRoomEvent: %v", err)
	}

	expectMessage(t, chA, store.RoomMessage{RoomID: roomA, Payload: payload})
	expectNoMessage(t, chB, 500*time.Millisecond)
}

// TestRoomSubscription_Unsubscribe confirms Unsubscribe actually stops
// delivery — the Hub drives it on a room's last-local-client-leaves
// transition (see internal/websocket/hub.go) and depends on it being real,
// not a no-op.
func TestRoomSubscription_Unsubscribe(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	room := uuid.New()

	sub := s.NewRoomSubscription(ctx)
	defer func() { _ = sub.Close() }()

	if err := sub.Subscribe(ctx, room); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	ch := sub.Messages()

	first := []byte(`{"seq":1}`)
	if err := s.PublishRoomEvent(ctx, room, first); err != nil {
		t.Fatalf("PublishRoomEvent: %v", err)
	}
	expectMessage(t, ch, store.RoomMessage{RoomID: room, Payload: first})

	if err := sub.Unsubscribe(ctx, room); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}

	second := []byte(`{"seq":2}`)
	if err := s.PublishRoomEvent(ctx, room, second); err != nil {
		t.Fatalf("PublishRoomEvent: %v", err)
	}
	expectNoMessage(t, ch, 500*time.Millisecond)
}
