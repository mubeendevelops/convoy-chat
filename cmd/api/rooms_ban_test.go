package main

import (
	"net/http"
	"testing"
)

// TestKickBansRejoin drives the full ban flow: an admin kick blocks the
// target's self-rejoin and hides the channel from their browse list, while an
// admin re-invite lifts the ban (the confirmed "admins keep a recovery path"
// behavior).
func TestKickBansRejoin(t *testing.T) {
	srv := newTestServer(t)
	alice := signupTestUser(t, srv, "ban_alice") // channel admin
	bob := signupTestUser(t, srv, "ban_bob")

	var room struct {
		ID string `json:"id"`
	}
	postJSON(t, srv, "/api/v1/rooms", alice.token, map[string]any{"type": "channel", "name": "ban-room"}, &room)

	// bob self-joins the public channel.
	if code := postJSONStatus(t, srv, "/api/v1/rooms/"+room.ID+"/join", bob.token, nil, nil); code != http.StatusCreated {
		t.Fatalf("initial self-join: got status %d, want 201", code)
	}

	// alice kicks bob.
	if code := deleteJSONStatus(t, srv, "/api/v1/rooms/"+room.ID+"/members/"+bob.id, alice.token); code != http.StatusOK {
		t.Fatalf("kick: got status %d, want 200", code)
	}

	t.Run("banned user cannot self-rejoin", func(t *testing.T) {
		if code := postJSONStatus(t, srv, "/api/v1/rooms/"+room.ID+"/join", bob.token, nil, nil); code != http.StatusForbidden {
			t.Errorf("self-rejoin after kick: got status %d, want 403", code)
		}
	})

	t.Run("banned channel is hidden from browse", func(t *testing.T) {
		var channels []publicChannel
		if code := getJSON(t, srv, "/api/v1/rooms/public", bob.token, &channels); code != http.StatusOK {
			t.Fatalf("browse: got status %d, want 200", code)
		}
		if containsChannelID(channels, room.ID) {
			t.Errorf("banned channel must not appear in browse list: %+v", channels)
		}
	})

	t.Run("admin re-invite lifts the ban", func(t *testing.T) {
		if code := postJSONStatus(t, srv, "/api/v1/rooms/"+room.ID+"/invite", alice.token, map[string]any{"user_id": bob.id}, nil); code != http.StatusCreated {
			t.Fatalf("re-invite: got status %d, want 201", code)
		}
		// bob is active again — a further self-join now 409s (already a member),
		// proving the ban was lifted rather than still blocking with a 403.
		if code := postJSONStatus(t, srv, "/api/v1/rooms/"+room.ID+"/join", bob.token, nil, nil); code != http.StatusConflict {
			t.Errorf("self-join after re-invite: got status %d, want 409", code)
		}
	})
}
