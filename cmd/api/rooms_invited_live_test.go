package main

import (
	"encoding/json"
	"testing"
	"time"
)

// TestRoomInvited_LiveOnDirectRoomCreation proves the fix for "a brand-new
// DM doesn't show up for its peer until they refresh": bob's WS connection
// is opened *before* the room exists and he never sends room.join for it
// (he can't — he doesn't know the room ID yet), so the only way he can learn
// about it live is the new per-user room.invited channel added alongside
// this test.
func TestRoomInvited_LiveOnDirectRoomCreation(t *testing.T) {
	srv := newTestServer(t)
	alice := signupTestUser(t, srv, "invited_live_alice")
	bob := signupTestUser(t, srv, "invited_live_bob")

	bobConn := dialTestWS(t, srv, bob)

	var dm roomResp
	postJSON(t, srv, "/api/v1/rooms", alice.token, map[string]any{"type": "direct", "peer_user_id": bob.id}, &dm)

	raw := readUntil(t, bobConn, "room.invited")
	var evt struct {
		Type   string `json:"type"`
		RoomID string `json:"room_id"`
	}
	if err := json.Unmarshal(raw, &evt); err != nil {
		t.Fatalf("decoding room.invited: %v", err)
	}
	if evt.RoomID != dm.ID {
		t.Errorf("got room.invited for %s, want %s", evt.RoomID, dm.ID)
	}
}

// TestRoomInvited_NotPublishedWhenDirectRoomAlreadyExists: re-creating an
// already-existing DM (the 200 dedup path) shouldn't re-announce it — the
// peer already learned about it the first time.
func TestRoomInvited_NotPublishedWhenDirectRoomAlreadyExists(t *testing.T) {
	srv := newTestServer(t)
	alice := signupTestUser(t, srv, "invited_dedup_alice")
	bob := signupTestUser(t, srv, "invited_dedup_bob")

	bobConn := dialTestWS(t, srv, bob)

	var first roomResp
	postJSON(t, srv, "/api/v1/rooms", alice.token, map[string]any{"type": "direct", "peer_user_id": bob.id}, &first)
	readUntil(t, bobConn, "room.invited") // the genuine first-creation announcement

	var second roomResp
	postJSON(t, srv, "/api/v1/rooms", alice.token, map[string]any{"type": "direct", "peer_user_id": bob.id}, &second)
	if second.ID != first.ID {
		t.Fatalf("re-creating the same DM: got a different room %s, want %s", second.ID, first.ID)
	}

	if err := bobConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
		t.Fatalf("setting read deadline: %v", err)
	}
	if _, data, err := bobConn.ReadMessage(); err == nil {
		t.Errorf("expected no further event after re-creating an existing DM, got: %s", data)
	}
}

// TestRoomInvited_LiveOnGroupCreation: every initial member_ids entry gets
// its own live room.invited, not just the creator's own view of the group.
func TestRoomInvited_LiveOnGroupCreation(t *testing.T) {
	srv := newTestServer(t)
	alice := signupTestUser(t, srv, "invited_group_alice")
	bob := signupTestUser(t, srv, "invited_group_bob")
	carol := signupTestUser(t, srv, "invited_group_carol")

	bobConn := dialTestWS(t, srv, bob)
	carolConn := dialTestWS(t, srv, carol)

	var group roomResp
	postJSON(t, srv, "/api/v1/rooms", alice.token, map[string]any{
		"type": "group", "name": "invited-live-group", "member_ids": []string{bob.id, carol.id},
	}, &group)

	bobRaw := readUntil(t, bobConn, "room.invited")
	var bobEvt struct {
		RoomID string `json:"room_id"`
	}
	if err := json.Unmarshal(bobRaw, &bobEvt); err != nil {
		t.Fatalf("decoding bob's room.invited: %v", err)
	}
	if bobEvt.RoomID != group.ID {
		t.Errorf("bob: got room.invited for %s, want %s", bobEvt.RoomID, group.ID)
	}

	carolRaw := readUntil(t, carolConn, "room.invited")
	var carolEvt struct {
		RoomID string `json:"room_id"`
	}
	if err := json.Unmarshal(carolRaw, &carolEvt); err != nil {
		t.Fatalf("decoding carol's room.invited: %v", err)
	}
	if carolEvt.RoomID != group.ID {
		t.Errorf("carol: got room.invited for %s, want %s", carolEvt.RoomID, group.ID)
	}
}

// TestRoomInvited_LiveOnInvite: an explicit invite into an existing channel
// also fires the live signal, same as a brand-new DM/group.
func TestRoomInvited_LiveOnInvite(t *testing.T) {
	srv := newTestServer(t)
	alice := signupTestUser(t, srv, "invited_invite_alice")
	bob := signupTestUser(t, srv, "invited_invite_bob")
	roomID := createTestChannel(t, srv, alice, "invited-invite-room")

	bobConn := dialTestWS(t, srv, bob)

	inviteTestUser(t, srv, alice, roomID, bob.id)

	raw := readUntil(t, bobConn, "room.invited")
	var evt struct {
		RoomID string `json:"room_id"`
	}
	if err := json.Unmarshal(raw, &evt); err != nil {
		t.Fatalf("decoding room.invited: %v", err)
	}
	if evt.RoomID != roomID {
		t.Errorf("got room.invited for %s, want %s", evt.RoomID, roomID)
	}
}
