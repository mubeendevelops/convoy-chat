package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestDirectRoom_ResumesAfterCallerLeaves is the end-to-end regression test
// for the "leaving a DM orphans it" bug: alice leaves her DM with bob, bob's
// view is untouched and he can keep sending into it, and alice starting a
// new conversation with bob afterward must resume the SAME room (200, not
// 201) rather than forking a disconnected duplicate — and must be able to
// see everything bob sent while she was away.
func TestDirectRoom_ResumesAfterCallerLeaves(t *testing.T) {
	srv := newTestServer(t)
	alice := signupTestUser(t, srv, "resume_alice")
	bob := signupTestUser(t, srv, "resume_bob")

	var original roomResp
	postJSON(t, srv, "/api/v1/rooms", alice.token, map[string]any{"type": "direct", "peer_user_id": bob.id}, &original)

	postJSON(t, srv, "/api/v1/rooms/"+original.ID+"/messages", bob.token, map[string]any{"content": "hi while you're still here"}, nil)

	if status := postJSONStatus(t, srv, "/api/v1/rooms/"+original.ID+"/leave", alice.token, nil, nil); status != http.StatusOK {
		t.Fatalf("alice leaving: got status %d, want 200", status)
	}

	// alice's own room list must no longer include it.
	var aliceRooms []roomResp
	getJSON(t, srv, "/api/v1/rooms", alice.token, &aliceRooms)
	for _, r := range aliceRooms {
		if r.ID == original.ID {
			t.Fatalf("alice's room list still contains the DM she just left: %s", r.ID)
		}
	}

	// bob's own view must be completely untouched: still present, still able
	// to send into it (nothing server-side should reject this).
	var bobRooms []roomResp
	getJSON(t, srv, "/api/v1/rooms", bob.token, &bobRooms)
	found := false
	for _, r := range bobRooms {
		if r.ID == original.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("bob's room list should still contain the DM alice left")
	}
	if status := postJSONStatus(t, srv, "/api/v1/rooms/"+original.ID+"/messages", bob.token, map[string]any{"content": "are you there?"}, nil); status != http.StatusCreated {
		t.Fatalf("bob sending into the room alice left: got status %d, want 201", status)
	}

	// alice starts a "new" conversation with bob — this must resume the
	// SAME room (200 dedup, not 201 creation), not fork a duplicate.
	var resumed roomResp
	status := postJSONStatus(t, srv, "/api/v1/rooms", alice.token, map[string]any{"type": "direct", "peer_user_id": bob.id}, &resumed)
	if status != http.StatusOK {
		t.Errorf("alice re-starting the DM: got status %d, want 200 (resuming, not creating)", status)
	}
	if resumed.ID != original.ID {
		t.Fatalf("got a different room id %s, want the original %s — leaving must not fork a duplicate DM", resumed.ID, original.ID)
	}

	// alice must be an active member again and see everything bob sent
	// while she was away, via the SAME room.
	var messages []struct {
		Content *string `json:"content"`
	}
	getJSON(t, srv, "/api/v1/rooms/"+original.ID+"/messages", alice.token, &messages)
	if len(messages) != 2 {
		t.Fatalf("got %d messages after resuming, want 2 (both of bob's)", len(messages))
	}

	// alice's room list must contain exactly one DM with bob — not two.
	var aliceRoomsAfter []roomResp
	getJSON(t, srv, "/api/v1/rooms", alice.token, &aliceRoomsAfter)
	directCount := 0
	for _, r := range aliceRoomsAfter {
		if r.Type == "direct" {
			directCount++
		}
	}
	if directCount != 1 {
		t.Errorf("alice has %d direct rooms after resuming, want exactly 1 (no duplicate)", directCount)
	}
}

// TestDirectRoom_NotifiesPeerLiveWhenPeerLeftAndIsResumed covers the mirror
// direction: bob (the peer, not the caller) is the one who left. When alice
// starts the conversation again, bob's own membership is silently restored
// server-side — his client has no other way to learn this than a live
// room.invited push, since his room list never looked any different to him.
func TestDirectRoom_NotifiesPeerLiveWhenPeerLeftAndIsResumed(t *testing.T) {
	srv := newTestServer(t)
	alice := signupTestUser(t, srv, "resume_notify_alice")
	bob := signupTestUser(t, srv, "resume_notify_bob")

	var original roomResp
	postJSON(t, srv, "/api/v1/rooms", alice.token, map[string]any{"type": "direct", "peer_user_id": bob.id}, &original)

	if status := postJSONStatus(t, srv, "/api/v1/rooms/"+original.ID+"/leave", bob.token, nil, nil); status != http.StatusOK {
		t.Fatalf("bob leaving: got status %d, want 200", status)
	}

	bobConn := dialTestWS(t, srv, bob)

	var resumed roomResp
	status := postJSONStatus(t, srv, "/api/v1/rooms", alice.token, map[string]any{"type": "direct", "peer_user_id": bob.id}, &resumed)
	if status != http.StatusOK {
		t.Errorf("alice re-starting the DM: got status %d, want 200 (resuming, not creating)", status)
	}
	if resumed.ID != original.ID {
		t.Fatalf("got a different room id %s, want the original %s", resumed.ID, original.ID)
	}

	raw := readUntil(t, bobConn, "room.invited")
	var evt struct {
		RoomID string `json:"room_id"`
	}
	if err := json.Unmarshal(raw, &evt); err != nil {
		t.Fatalf("decoding room.invited: %v", err)
	}
	if evt.RoomID != original.ID {
		t.Errorf("got room.invited for %s, want %s", evt.RoomID, original.ID)
	}
}
