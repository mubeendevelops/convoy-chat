package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// patchJSONStatus mirrors postJSONStatus (rooms_join_test.go) but for PATCH —
// tests here need to assert on 403/404/400, not just the happy path.
func patchJSONStatus(t *testing.T, srv *httptest.Server, path, token string, body any, out any) int {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshaling request body: %v", err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPatch, srv.URL+path, bytes.NewReader(data))
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("request to %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if out != nil && resp.StatusCode < 300 {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decoding response from %s: %v", path, err)
		}
	}
	return resp.StatusCode
}

type editMessageResp struct {
	ID       string `json:"id"`
	RoomID   string `json:"room_id"`
	Content  string `json:"content"`
	EditedAt string `json:"edited_at"`
}

// TestEditMessage_AuthorHappyPathAndLiveBroadcast drives the full author-edit
// flow through the real router + WS stack: the author edits their own
// message, gets back the new content/edited_at, and another member already
// watching the room receives message.edited live.
func TestEditMessage_AuthorHappyPathAndLiveBroadcast(t *testing.T) {
	srv := newTestServer(t)
	alice := signupTestUser(t, srv, "edit_alice")
	bob := signupTestUser(t, srv, "edit_bob")
	roomID := createTestChannel(t, srv, alice, "edit-room")
	inviteTestUser(t, srv, alice, roomID, bob.id)

	var sendResp struct {
		ID string `json:"id"`
	}
	postJSON(t, srv, "/api/v1/rooms/"+roomID+"/messages", alice.token, map[string]any{"content": "original"}, &sendResp)

	bobConn := dialTestWS(t, srv, bob)
	sendWS(t, bobConn, map[string]any{"type": "room.join", "room_id": roomID})
	readUntil(t, bobConn, "user.joined") // wait for the join to be acked before editing

	var editResp editMessageResp
	status := patchJSONStatus(t, srv, "/api/v1/messages/"+sendResp.ID, alice.token, map[string]any{"content": "edited by author"}, &editResp)
	if status != http.StatusOK {
		t.Fatalf("author edit: got status %d, want 200", status)
	}
	if editResp.Content != "edited by author" {
		t.Errorf("got content %q, want %q", editResp.Content, "edited by author")
	}
	if editResp.EditedAt == "" {
		t.Error("expected a non-empty edited_at")
	}
	if editResp.ID != sendResp.ID || editResp.RoomID != roomID {
		t.Errorf("got id=%s room_id=%s, want id=%s room_id=%s", editResp.ID, editResp.RoomID, sendResp.ID, roomID)
	}

	raw := readUntil(t, bobConn, "message.edited")
	var evt struct {
		Type     string `json:"type"`
		ID       string `json:"id"`
		RoomID   string `json:"room_id"`
		Content  string `json:"content"`
		EditedAt string `json:"edited_at"`
	}
	if err := json.Unmarshal(raw, &evt); err != nil {
		t.Fatalf("decoding message.edited: %v", err)
	}
	if evt.ID != sendResp.ID || evt.RoomID != roomID || evt.Content != "edited by author" || evt.EditedAt == "" {
		t.Errorf("unexpected message.edited payload: %+v", evt)
	}
}

// TestEditMessage_NonAuthorForbidden confirms edit is author-only with NO
// admin override — a deliberate asymmetry with DELETE, where a room admin
// may remove someone else's message but may not rewrite it.
func TestEditMessage_NonAuthorForbidden(t *testing.T) {
	srv := newTestServer(t)
	alice := signupTestUser(t, srv, "edit_owner")
	bob := signupTestUser(t, srv, "edit_admin")
	roomID := createTestChannel(t, srv, bob, "admin-room") // bob is the creator -> room admin
	inviteTestUser(t, srv, bob, roomID, alice.id)

	var sendResp struct {
		ID string `json:"id"`
	}
	postJSON(t, srv, "/api/v1/rooms/"+roomID+"/messages", alice.token, map[string]any{"content": "alice's message"}, &sendResp)

	// bob is a room admin (could DELETE this message) but must NOT be able to edit it.
	if status := patchJSONStatus(t, srv, "/api/v1/messages/"+sendResp.ID, bob.token, map[string]any{"content": "hijacked"}, nil); status != http.StatusForbidden {
		t.Errorf("admin (non-author) edit: got status %d, want 403", status)
	}
}

func TestEditMessage_DeletedMessageNotFound(t *testing.T) {
	srv := newTestServer(t)
	alice := signupTestUser(t, srv, "edit_deleter")
	roomID := createTestChannel(t, srv, alice, "delete-then-edit-room")

	var sendResp struct {
		ID string `json:"id"`
	}
	postJSON(t, srv, "/api/v1/rooms/"+roomID+"/messages", alice.token, map[string]any{"content": "will be deleted"}, &sendResp)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodDelete, srv.URL+"/api/v1/messages/"+sendResp.ID, nil)
	if err != nil {
		t.Fatalf("building delete request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+alice.token)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("deleting message: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("deleting fixture message: got status %d, want 200", resp.StatusCode)
	}

	if status := patchJSONStatus(t, srv, "/api/v1/messages/"+sendResp.ID, alice.token, map[string]any{"content": "resurrect me"}, nil); status != http.StatusNotFound {
		t.Errorf("editing a deleted message: got status %d, want 404", status)
	}
}

func TestEditMessage_NonexistentMessageNotFound(t *testing.T) {
	srv := newTestServer(t)
	alice := signupTestUser(t, srv, "edit_ghost")

	if status := patchJSONStatus(t, srv, "/api/v1/messages/00000000-0000-0000-0000-000000000000", alice.token, map[string]any{"content": "irrelevant"}, nil); status != http.StatusNotFound {
		t.Errorf("editing a nonexistent message: got status %d, want 404", status)
	}
}

func TestEditMessage_InvalidContent(t *testing.T) {
	srv := newTestServer(t)
	alice := signupTestUser(t, srv, "edit_validator")
	roomID := createTestChannel(t, srv, alice, "validate-room")

	var sendResp struct {
		ID string `json:"id"`
	}
	postJSON(t, srv, "/api/v1/rooms/"+roomID+"/messages", alice.token, map[string]any{"content": "valid"}, &sendResp)

	if status := patchJSONStatus(t, srv, "/api/v1/messages/"+sendResp.ID, alice.token, map[string]any{"content": ""}, nil); status != http.StatusBadRequest {
		t.Errorf("empty content: got status %d, want 400", status)
	}
	if status := patchJSONStatus(t, srv, "/api/v1/messages/"+sendResp.ID, alice.token, map[string]any{"content": "   "}, nil); status != http.StatusBadRequest {
		t.Errorf("whitespace-only content: got status %d, want 400", status)
	}
}

func TestEditMessage_RequiresAuth(t *testing.T) {
	srv := newTestServer(t)
	alice := signupTestUser(t, srv, "edit_needsauth")
	roomID := createTestChannel(t, srv, alice, "auth-room")

	var sendResp struct {
		ID string `json:"id"`
	}
	postJSON(t, srv, "/api/v1/rooms/"+roomID+"/messages", alice.token, map[string]any{"content": "hi"}, &sendResp)

	if status := patchJSONStatus(t, srv, "/api/v1/messages/"+sendResp.ID, "", map[string]any{"content": "no auth"}, nil); status != http.StatusUnauthorized {
		t.Errorf("no Bearer token: got status %d, want 401", status)
	}
}
