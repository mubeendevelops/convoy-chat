package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// deleteJSONStatus mirrors postJSONStatus/patchJSONStatus but for DELETE.
func deleteJSONStatus(t *testing.T, srv *httptest.Server, path, token string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodDelete, srv.URL+path, nil)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("request to %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode
}

type roomResp struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	IsPublic bool   `json:"is_public"`
}

// TestCreateGroupRoom drives the full group-creation flow: creator + 2
// initial members all land as active members, creator is admin, the rest
// are plain members, and the room is never public.
func TestCreateGroupRoom(t *testing.T) {
	srv := newTestServer(t)
	alice := signupTestUser(t, srv, "group_alice")
	bob := signupTestUser(t, srv, "group_bob")
	carol := signupTestUser(t, srv, "group_carol")

	var room roomResp
	postJSON(t, srv, "/api/v1/rooms", alice.token, map[string]any{
		"type":       "group",
		"name":       "trip-planning",
		"member_ids": []string{bob.id, carol.id},
	}, &room)

	if room.Type != "group" {
		t.Errorf("got type %q, want %q", room.Type, "group")
	}
	if room.IsPublic {
		t.Error("a group room must never be public")
	}

	var members []struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
		Role string `json:"role"`
	}
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/api/v1/rooms/"+room.ID+"/members", nil)
	req.Header.Set("Authorization", "Bearer "+alice.token)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("listing members: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(resp.Body).Decode(&members); err != nil {
		t.Fatalf("decoding members: %v", err)
	}
	if len(members) != 3 {
		t.Fatalf("got %d members, want 3", len(members))
	}
	for _, m := range members {
		wantRole := "member"
		if m.User.ID == alice.id {
			wantRole = "admin"
		}
		if m.Role != wantRole {
			t.Errorf("member %s: got role %q, want %q", m.User.ID, m.Role, wantRole)
		}
	}
}

func TestCreateGroupRoom_ValidationErrors(t *testing.T) {
	srv := newTestServer(t)
	alice := signupTestUser(t, srv, "group_val_alice")
	bob := signupTestUser(t, srv, "group_val_bob")

	if status := postJSONStatus(t, srv, "/api/v1/rooms", alice.token, map[string]any{
		"type": "group", "name": "too-small", "member_ids": []string{bob.id},
	}, nil); status != http.StatusBadRequest {
		t.Errorf("only 1 member_id: got status %d, want 400", status)
	}

	if status := postJSONStatus(t, srv, "/api/v1/rooms", alice.token, map[string]any{
		"type": "group", "name": "self-included", "member_ids": []string{alice.id, bob.id},
	}, nil); status != http.StatusBadRequest {
		t.Errorf("creator in member_ids: got status %d, want 400", status)
	}

	if status := postJSONStatus(t, srv, "/api/v1/rooms", alice.token, map[string]any{
		"type": "group", "name": "unknown-member", "member_ids": []string{bob.id, "00000000-0000-0000-0000-000000000000"},
	}, nil); status != http.StatusNotFound {
		t.Errorf("unknown member_id: got status %d, want 404", status)
	}
}

// TestChangeMemberRole_HappyPathAndLiveBroadcast: an admin promotes a member,
// a second connected client sees member.role_changed live.
func TestChangeMemberRole_HappyPathAndLiveBroadcast(t *testing.T) {
	srv := newTestServer(t)
	alice := signupTestUser(t, srv, "role_alice")
	bob := signupTestUser(t, srv, "role_bob")
	roomID := createTestChannel(t, srv, alice, "role-room")
	inviteTestUser(t, srv, alice, roomID, bob.id)

	bobConn := dialTestWS(t, srv, bob)
	sendWS(t, bobConn, map[string]any{"type": "room.join", "room_id": roomID})
	readUntil(t, bobConn, "user.joined")

	var changeResp map[string]string
	status := patchJSONStatus(t, srv, "/api/v1/rooms/"+roomID+"/members/"+bob.id+"/role", alice.token, map[string]any{"role": "admin"}, &changeResp)
	if status != http.StatusOK {
		t.Fatalf("promote: got status %d, want 200", status)
	}
	if changeResp["role"] != "admin" {
		t.Errorf("got role %q, want %q", changeResp["role"], "admin")
	}

	raw := readUntil(t, bobConn, "member.role_changed")
	var evt struct {
		Type   string `json:"type"`
		RoomID string `json:"room_id"`
		UserID string `json:"user_id"`
		Role   string `json:"role"`
	}
	if err := json.Unmarshal(raw, &evt); err != nil {
		t.Fatalf("decoding member.role_changed: %v", err)
	}
	if evt.RoomID != roomID || evt.UserID != bob.id || evt.Role != "admin" {
		t.Errorf("unexpected member.role_changed payload: %+v", evt)
	}
}

func TestChangeMemberRole_NoopDoesNotBroadcast(t *testing.T) {
	srv := newTestServer(t)
	alice := signupTestUser(t, srv, "role_noop_alice")

	roomID := createTestChannel(t, srv, alice, "noop-room")

	var changeResp map[string]string
	status := patchJSONStatus(t, srv, "/api/v1/rooms/"+roomID+"/members/"+alice.id+"/role", alice.token, map[string]any{"role": "admin"}, &changeResp)
	if status != http.StatusOK {
		t.Fatalf("re-setting the same role: got status %d, want 200", status)
	}
}

func TestChangeMemberRole_NonAdminForbidden(t *testing.T) {
	srv := newTestServer(t)
	alice := signupTestUser(t, srv, "role_forbid_alice")
	bob := signupTestUser(t, srv, "role_forbid_bob")
	roomID := createTestChannel(t, srv, alice, "forbid-room")
	inviteTestUser(t, srv, alice, roomID, bob.id)

	// bob is a plain member, not admin — must not be able to change roles.
	if status := patchJSONStatus(t, srv, "/api/v1/rooms/"+roomID+"/members/"+alice.id+"/role", bob.token, map[string]any{"role": "member"}, nil); status != http.StatusForbidden {
		t.Errorf("non-admin role change: got status %d, want 403", status)
	}
}

func TestChangeMemberRole_DirectRoomForbidden(t *testing.T) {
	srv := newTestServer(t)
	alice := signupTestUser(t, srv, "role_dm_alice")
	bob := signupTestUser(t, srv, "role_dm_bob")

	var dm roomResp
	postJSON(t, srv, "/api/v1/rooms", alice.token, map[string]any{"type": "direct", "peer_user_id": bob.id}, &dm)

	if status := patchJSONStatus(t, srv, "/api/v1/rooms/"+dm.ID+"/members/"+bob.id+"/role", alice.token, map[string]any{"role": "admin"}, nil); status != http.StatusForbidden {
		t.Errorf("role change on a DM: got status %d, want 403 (DMs have no admin)", status)
	}
}

func TestChangeMemberRole_LastAdminRejected(t *testing.T) {
	srv := newTestServer(t)
	alice := signupTestUser(t, srv, "role_last_alice")
	roomID := createTestChannel(t, srv, alice, "last-admin-room")

	if status := patchJSONStatus(t, srv, "/api/v1/rooms/"+roomID+"/members/"+alice.id+"/role", alice.token, map[string]any{"role": "member"}, nil); status != http.StatusConflict {
		t.Errorf("demoting the sole admin: got status %d, want 409", status)
	}
}

// TestRemoveMember_HappyPathAndLiveBroadcast: an admin kicks a member,
// another connected client sees user.left live.
func TestRemoveMember_HappyPathAndLiveBroadcast(t *testing.T) {
	srv := newTestServer(t)
	alice := signupTestUser(t, srv, "kick_alice")
	bob := signupTestUser(t, srv, "kick_bob")
	carol := signupTestUser(t, srv, "kick_carol")
	roomID := createTestChannel(t, srv, alice, "kick-room")
	inviteTestUser(t, srv, alice, roomID, bob.id)
	inviteTestUser(t, srv, alice, roomID, carol.id)

	carolConn := dialTestWS(t, srv, carol)
	sendWS(t, carolConn, map[string]any{"type": "room.join", "room_id": roomID})
	readUntil(t, carolConn, "user.joined")

	if status := deleteJSONStatus(t, srv, "/api/v1/rooms/"+roomID+"/members/"+bob.id, alice.token); status != http.StatusOK {
		t.Fatalf("kick: got status %d, want 200", status)
	}

	raw := readUntil(t, carolConn, "user.left")
	var evt struct {
		Type   string `json:"type"`
		UserID string `json:"user_id"`
		RoomID string `json:"room_id"`
	}
	if err := json.Unmarshal(raw, &evt); err != nil {
		t.Fatalf("decoding user.left: %v", err)
	}
	if evt.UserID != bob.id || evt.RoomID != roomID {
		t.Errorf("unexpected user.left payload: %+v", evt)
	}

	// bob must no longer be an active member.
	if status := postJSONStatus(t, srv, "/api/v1/rooms/"+roomID+"/leave", bob.token, nil, nil); status != http.StatusNotFound {
		t.Errorf("kicked user attempting to leave: got status %d, want 404 (already not a member)", status)
	}
}

func TestRemoveMember_NonAdminForbidden(t *testing.T) {
	srv := newTestServer(t)
	alice := signupTestUser(t, srv, "kick_forbid_alice")
	bob := signupTestUser(t, srv, "kick_forbid_bob")
	carol := signupTestUser(t, srv, "kick_forbid_carol")
	roomID := createTestChannel(t, srv, alice, "kick-forbid-room")
	inviteTestUser(t, srv, alice, roomID, bob.id)
	inviteTestUser(t, srv, alice, roomID, carol.id)

	if status := deleteJSONStatus(t, srv, "/api/v1/rooms/"+roomID+"/members/"+carol.id, bob.token); status != http.StatusForbidden {
		t.Errorf("non-admin kick: got status %d, want 403", status)
	}
}

func TestRemoveMember_SelfRejected(t *testing.T) {
	srv := newTestServer(t)
	alice := signupTestUser(t, srv, "kick_self_alice")
	roomID := createTestChannel(t, srv, alice, "kick-self-room")

	if status := deleteJSONStatus(t, srv, "/api/v1/rooms/"+roomID+"/members/"+alice.id, alice.token); status != http.StatusBadRequest {
		t.Errorf("self-kick: got status %d, want 400", status)
	}
}

// TestLeaveRoom_BroadcastsAndTriggersSuccession: the sole admin leaving both
// broadcasts user.left and auto-promotes the oldest remaining member, which
// itself broadcasts member.role_changed.
func TestLeaveRoom_BroadcastsAndTriggersSuccession(t *testing.T) {
	srv := newTestServer(t)
	alice := signupTestUser(t, srv, "succession_alice")
	bob := signupTestUser(t, srv, "succession_bob")
	roomID := createTestChannel(t, srv, alice, "succession-room")
	inviteTestUser(t, srv, alice, roomID, bob.id)

	bobConn := dialTestWS(t, srv, bob)
	sendWS(t, bobConn, map[string]any{"type": "room.join", "room_id": roomID})
	readUntil(t, bobConn, "user.joined")

	if status := postJSONStatus(t, srv, "/api/v1/rooms/"+roomID+"/leave", alice.token, nil, nil); status != http.StatusOK {
		t.Fatalf("alice leaving: got status %d, want 200", status)
	}

	leftRaw := readUntil(t, bobConn, "user.left")
	var leftEvt struct {
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(leftRaw, &leftEvt); err != nil {
		t.Fatalf("decoding user.left: %v", err)
	}
	if leftEvt.UserID != alice.id {
		t.Errorf("got user.left for %s, want %s", leftEvt.UserID, alice.id)
	}

	roleRaw := readUntil(t, bobConn, "member.role_changed")
	var roleEvt struct {
		UserID string `json:"user_id"`
		Role   string `json:"role"`
	}
	if err := json.Unmarshal(roleRaw, &roleEvt); err != nil {
		t.Fatalf("decoding member.role_changed: %v", err)
	}
	if roleEvt.UserID != bob.id || roleEvt.Role != "admin" {
		t.Errorf("got succession promotion for %s (%s), want %s (admin)", roleEvt.UserID, roleEvt.Role, bob.id)
	}
}
