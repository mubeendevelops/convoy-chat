package websocket

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// typingKey identifies one user's typing session within one room.
type typingKey struct {
	RoomID uuid.UUID
	UserID uuid.UUID
}

// typingTracker holds a short-lived timer per (room, user) so a dropped
// typing.stop doesn't leave someone "typing forever" — see CLAUDE.md's WS
// event contract. This state doesn't fit the Hub's connection/room model (it
// isn't keyed by connection, and its timers fire on their own goroutines), so
// unlike the Hub it's guarded by a plain mutex rather than a single owning
// goroutine — a deliberate, narrowly-scoped exception, not a departure from
// the "no mutexes scattered around" convention.
type typingTracker struct {
	mu     sync.Mutex
	timers map[typingKey]*time.Timer
}

func newTypingTracker() *typingTracker {
	return &typingTracker{timers: make(map[typingKey]*time.Timer)}
}

// Start (re)starts key's auto-expire timer, calling onExpire if nothing stops
// or refreshes it within typingTimeout. Safe to call repeatedly while a user
// keeps typing — each call just pushes the deadline back out.
func (t *typingTracker) Start(key typingKey, onExpire func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if existing, ok := t.timers[key]; ok {
		existing.Stop()
	}
	t.timers[key] = time.AfterFunc(typingTimeout, func() {
		t.mu.Lock()
		delete(t.timers, key)
		t.mu.Unlock()
		onExpire()
	})
}

// Stop cancels key's timer (an explicit typing.stop), if one is running.
func (t *typingTracker) Stop(key typingKey) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if existing, ok := t.timers[key]; ok {
		existing.Stop()
		delete(t.timers, key)
	}
}
