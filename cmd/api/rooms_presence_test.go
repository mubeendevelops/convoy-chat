package main

import (
	"net/http"
	"testing"
)

type roomPresenceEntry struct {
	UserID string `json:"user_id"`
	Status string `json:"status"`
}

func presenceStatusFor(entries []roomPresenceEntry, userID string) string {
	for _, e := range entries {
		if e.UserID == userID {
			return e.Status
		}
	}
	return ""
}

// TestRoomPresence_SnapshotHydration confirms GET /rooms/{id}/presence returns
// a live-connected member as online and a never-connected one as offline (the
// snapshot that lets a client hydrate a peer's status on room open rather than
// only learning it from a live event), and refuses a non-member.
func TestRoomPresence_SnapshotHydration(t *testing.T) {
	srv := newTestServer(t)
	alice := signupTestUser(t, srv, "pres_alice")
	bob := signupTestUser(t, srv, "pres_bob")
	roomID := createTestChannel(t, srv, alice, "pres-room")
	inviteTestUser(t, srv, alice, roomID, bob.id)

	// bob connects and joins — once his room.join is acked, his connect-time
	// presenceOnline (which runs before readPump starts) is guaranteed done, so
	// the snapshot below won't race it.
	bobConn := dialTestWS(t, srv, bob)
	sendWS(t, bobConn, map[string]any{"type": "room.join", "room_id": roomID})
	readUntil(t, bobConn, "user.joined")

	var entries []roomPresenceEntry
	if code := getJSON(t, srv, "/api/v1/rooms/"+roomID+"/presence", alice.token, &entries); code != http.StatusOK {
		t.Fatalf("got status %d, want 200", code)
	}
	if got := presenceStatusFor(entries, bob.id); got != "online" {
		t.Errorf("bob (connected): got status %q, want online", got)
	}
	// alice never opened a socket, so she has no live presence entry — offline.
	if got := presenceStatusFor(entries, alice.id); got != "offline" {
		t.Errorf("alice (no socket): got status %q, want offline", got)
	}

	t.Run("non-member is refused", func(t *testing.T) {
		carol := signupTestUser(t, srv, "pres_carol")
		if code := getJSON(t, srv, "/api/v1/rooms/"+roomID+"/presence", carol.token, nil); code != http.StatusForbidden {
			t.Errorf("got status %d, want 403", code)
		}
	})
}
