package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestDeleteMessage_LiveBroadcast confirms a delete now reaches other clients
// live via message.deleted (closing the previously documented gap where a
// delete only surfaced on the other client's next history refetch/resync).
func TestDeleteMessage_LiveBroadcast(t *testing.T) {
	srv := newTestServer(t)
	alice := signupTestUser(t, srv, "del_alice")
	bob := signupTestUser(t, srv, "del_bob")
	roomID := createTestChannel(t, srv, alice, "del-room")
	inviteTestUser(t, srv, alice, roomID, bob.id)

	var sendResp struct {
		ID string `json:"id"`
	}
	postJSON(t, srv, "/api/v1/rooms/"+roomID+"/messages", alice.token, map[string]any{"content": "delete me"}, &sendResp)

	bobConn := dialTestWS(t, srv, bob)
	sendWS(t, bobConn, map[string]any{"type": "room.join", "room_id": roomID})
	readUntil(t, bobConn, "user.joined") // wait for the join to be acked before deleting

	if status := deleteJSONStatus(t, srv, "/api/v1/messages/"+sendResp.ID, alice.token); status != http.StatusOK {
		t.Fatalf("delete: got status %d, want 200", status)
	}

	raw := readUntil(t, bobConn, "message.deleted")
	var evt struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		RoomID    string `json:"room_id"`
		DeletedAt string `json:"deleted_at"`
	}
	if err := json.Unmarshal(raw, &evt); err != nil {
		t.Fatalf("decoding message.deleted: %v", err)
	}
	if evt.ID != sendResp.ID || evt.RoomID != roomID || evt.DeletedAt == "" {
		t.Errorf("unexpected message.deleted payload: %+v", evt)
	}

	// A second delete 404s (already soft-deleted) — the broadcast doesn't
	// change that idempotency.
	if status := deleteJSONStatus(t, srv, "/api/v1/messages/"+sendResp.ID, alice.token); status != http.StatusNotFound {
		t.Errorf("re-delete: got status %d, want 404", status)
	}
}
