package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// listRoomsUnread fetches the caller's room list and returns the unread_count
// for the given room id (fatals if the room isn't present).
func listRoomsUnread(t *testing.T, srv *httptest.Server, token, roomID string) int {
	t.Helper()
	var rooms []struct {
		ID          string `json:"id"`
		UnreadCount int    `json:"unread_count"`
	}
	getJSON(t, srv, "/api/v1/rooms", token, &rooms)
	for _, r := range rooms {
		if r.ID == roomID {
			return r.UnreadCount
		}
	}
	t.Fatalf("room %s not found in GET /rooms", roomID)
	return 0
}

// TestMarkRoomRead drives the unread lifecycle over the real router: a member's
// unread_count rises as another user sends messages, POST /rooms/{id}/read
// clears it, and a non-member gets 404.
func TestMarkRoomRead(t *testing.T) {
	srv := newTestServer(t)

	alice := signupTestUser(t, srv, "alice_read")
	bob := signupTestUser(t, srv, "bob_read")
	outsider := signupTestUser(t, srv, "outsider_read")

	roomID := createTestChannel(t, srv, alice, "read-room")
	inviteTestUser(t, srv, alice, roomID, bob.id)

	// Alice sends two messages; bob (who hasn't opened the room) sees unread=2,
	// alice's own view stays at 0.
	postJSON(t, srv, "/api/v1/rooms/"+roomID+"/messages", alice.token, map[string]any{"content": "hi"}, nil)
	postJSON(t, srv, "/api/v1/rooms/"+roomID+"/messages", alice.token, map[string]any{"content": "there"}, nil)

	if got := listRoomsUnread(t, srv, bob.token, roomID); got != 2 {
		t.Errorf("bob unread = %d, want 2", got)
	}
	if got := listRoomsUnread(t, srv, alice.token, roomID); got != 0 {
		t.Errorf("alice (own messages) unread = %d, want 0", got)
	}

	// Bob marks the room read → unread drops to 0.
	if code := postJSONStatus(t, srv, "/api/v1/rooms/"+roomID+"/read", bob.token, nil, nil); code != http.StatusOK {
		t.Fatalf("mark read: got status %d, want 200", code)
	}
	if got := listRoomsUnread(t, srv, bob.token, roomID); got != 0 {
		t.Errorf("bob unread after read = %d, want 0", got)
	}

	// A message after the cursor counts again.
	postJSON(t, srv, "/api/v1/rooms/"+roomID+"/messages", alice.token, map[string]any{"content": "more"}, nil)
	if got := listRoomsUnread(t, srv, bob.token, roomID); got != 1 {
		t.Errorf("bob unread after new message = %d, want 1", got)
	}

	// A non-member can't mark the room read.
	if code := postJSONStatus(t, srv, "/api/v1/rooms/"+roomID+"/read", outsider.token, nil, nil); code != http.StatusForbidden {
		t.Errorf("outsider mark read: got status %d, want 403", code)
	}
}
