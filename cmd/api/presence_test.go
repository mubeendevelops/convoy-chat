package main

import (
	"encoding/json"
	"testing"
	"time"

	gws "github.com/gorilla/websocket"
)

type presenceFrame struct {
	Type   string `json:"type"`
	UserID string `json:"user_id"`
	Status string `json:"status"`
}

// wsFrameReader continuously reads frames off a connection on a background
// goroutine into a buffered channel. This exists because the obvious
// alternative — calling ReadMessage with a per-call read deadline and
// treating the timeout as "no more frames" — poisons a gorilla/websocket
// connection: once a read deadline fires mid-frame the read side is left in
// an undefined state, so every subsequent read on that same connection fails
// immediately. The presence tests need to observe *absence* of an event
// (silence) as well as its presence, which fundamentally requires
// reading-until-some-window-elapses; doing that against the connection
// directly would corrupt it for the next assertion. Reading continuously
// into a channel and applying the time window to the channel receive instead
// keeps the connection healthy for the whole test.
type wsFrameReader struct {
	frames chan presenceFrame
}

func newWSFrameReader(conn *gws.Conn) *wsFrameReader {
	r := &wsFrameReader{frames: make(chan presenceFrame, 256)}
	go func() {
		defer close(r.frames)
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return // connection closed (test teardown) or genuine error
			}
			var f presenceFrame
			if json.Unmarshal(data, &f) == nil {
				r.frames <- f
			}
		}
	}()
	return r
}

// statusesFor collects every user.status_changed status for userID that
// arrives within the window, in arrival order. A window that elapses with no
// matching frame yields an empty slice — which is how the tests assert
// silence (e.g. a second connection must not re-announce "online").
func (r *wsFrameReader) statusesFor(userID string, within time.Duration) []string {
	deadline := time.After(within)
	var statuses []string
	for {
		select {
		case f, ok := <-r.frames:
			if !ok {
				return statuses
			}
			if f.Type == "user.status_changed" && f.UserID == userID {
				statuses = append(statuses, f.Status)
			}
		case <-deadline:
			return statuses
		}
	}
}

// drain discards whatever has arrived (or arrives within the window) — used
// to settle a connection's own connect-time noise before the interesting
// part of a test begins.
func (r *wsFrameReader) drain(within time.Duration) {
	deadline := time.After(within)
	for {
		select {
		case _, ok := <-r.frames:
			if !ok {
				return
			}
		case <-deadline:
			return
		}
	}
}

// TestPresence_ConnectOnlineOrderedBeforeImmediateUpdate is a regression test
// for a real race found while building this suite: presenceOnline (the
// connect-time "online" announcement) used to fire from a detached goroutine
// with no ordering relative to the same connection's very next command. A
// client that sent presence.update immediately after connecting could have
// the update's broadcast win the race and arrive first, so an observer would
// see "away", then "online" arriving late and incorrectly clobbering it.
// Fixed by running presenceOnline synchronously before readPump starts
// accepting inbound frames (see server.go's Handler), so a client's own
// actions can never be dispatched ahead of its connect announcement. This
// test sends the update with zero delay after connecting to maximize the
// chance of catching a regression.
func TestPresence_ConnectOnlineOrderedBeforeImmediateUpdate(t *testing.T) {
	srv := newTestServer(t)

	alice := signupTestUser(t, srv, "presence_ord_alice")
	bob := signupTestUser(t, srv, "presence_ord_bob")
	room := createTestChannel(t, srv, alice, "presence-order-room")
	inviteTestUser(t, srv, alice, room, bob.id)

	bobConn := dialTestWS(t, srv, bob)
	bobReader := newWSFrameReader(bobConn)
	sendWS(t, bobConn, map[string]any{"type": "room.join", "room_id": room})
	bobReader.drain(500 * time.Millisecond) // let bob's own connect settle before alice acts

	aliceConn := dialTestWS(t, srv, alice)
	sendWS(t, aliceConn, map[string]any{"type": "room.join", "room_id": room})
	sendWS(t, aliceConn, map[string]any{"type": "presence.update", "status": "away"})

	statuses := bobReader.statusesFor(alice.id, 3*time.Second)
	if len(statuses) != 2 || statuses[0] != "online" || statuses[1] != "away" {
		t.Fatalf("got alice's status_changed sequence %v, want exactly [online away] in that order", statuses)
	}
}

// TestPresence_ConnectionCountDrivenTransitions covers the multi-tab scenario
// manually verified in Phase 6: only a 0→1 connect or a →0 disconnect
// announces a status change; a second connection or a partial disconnect (one
// of several tabs closing) stays silent.
func TestPresence_ConnectionCountDrivenTransitions(t *testing.T) {
	srv := newTestServer(t)

	alice := signupTestUser(t, srv, "presence_mt_alice")
	bob := signupTestUser(t, srv, "presence_mt_bob")
	room := createTestChannel(t, srv, alice, "presence-multitab-room")
	inviteTestUser(t, srv, alice, room, bob.id)

	bobConn := dialTestWS(t, srv, bob)
	bobReader := newWSFrameReader(bobConn)
	sendWS(t, bobConn, map[string]any{"type": "room.join", "room_id": room})
	bobReader.drain(500 * time.Millisecond)

	aliceConn1 := dialTestWS(t, srv, alice)
	sendWS(t, aliceConn1, map[string]any{"type": "room.join", "room_id": room})
	if statuses := bobReader.statusesFor(alice.id, 2*time.Second); len(statuses) != 1 || statuses[0] != "online" {
		t.Fatalf("after alice's first connection, got %v, want exactly [online]", statuses)
	}

	aliceConn2 := dialTestWS(t, srv, alice)
	sendWS(t, aliceConn2, map[string]any{"type": "room.join", "room_id": room})
	if statuses := bobReader.statusesFor(alice.id, 1*time.Second); len(statuses) != 0 {
		t.Errorf("a second connection (another tab) must not re-announce status, got %v", statuses)
	}

	_ = aliceConn1.Close()
	if statuses := bobReader.statusesFor(alice.id, 1*time.Second); len(statuses) != 0 {
		t.Errorf("closing one of two connections must not announce a status change, got %v", statuses)
	}

	_ = aliceConn2.Close()
	if statuses := bobReader.statusesFor(alice.id, 2*time.Second); len(statuses) != 1 || statuses[0] != "offline" {
		t.Fatalf("after alice's last connection closes, got %v, want exactly [offline]", statuses)
	}
}
