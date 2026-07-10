package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// postJSONStatus issues an authenticated POST and returns the status code,
// decoding the body into out when out is non-nil and the status is 2xx.
// Unlike postJSON (which fatals on any non-2xx, since every existing caller
// only drives happy paths), this is for tests that need to assert on an
// error status such as 403/409.
func postJSONStatus(t *testing.T, srv *httptest.Server, path, token string, body any, out any) int {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshaling request body: %v", err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+path, bytes.NewReader(data))
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

type publicChannel struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	MemberCount int    `json:"member_count"`
}

func containsChannelID(channels []publicChannel, id string) bool {
	for _, c := range channels {
		if c.ID == id {
			return true
		}
	}
	return false
}

// TestBrowseAndJoinPublicChannel drives the full Phase 2 flow: a public
// channel shows up in GET /rooms/public for a non-member, self-join via
// POST .../join adds them and broadcasts user.joined to an already-open
// room, a second join attempt 409s, and a private channel is neither listed
// nor joinable.
func TestBrowseAndJoinPublicChannel(t *testing.T) {
	srv := newTestServer(t)

	alice := signupTestUser(t, srv, "alice_join")
	bob := signupTestUser(t, srv, "bob_join")

	var room struct {
		ID string `json:"id"`
	}
	postJSON(t, srv, "/api/v1/rooms", alice.token, map[string]any{"type": "channel", "name": "join-room"}, &room)

	t.Run("appears in the browse list for a non-member, with the creator's member count", func(t *testing.T) {
		var channels []publicChannel
		code := getJSON(t, srv, "/api/v1/rooms/public", bob.token, &channels)
		if code != http.StatusOK {
			t.Fatalf("got status %d, want 200", code)
		}
		if !containsChannelID(channels, room.ID) {
			t.Fatalf("got %+v, want it to include room %s", channels, room.ID)
		}
		for _, c := range channels {
			if c.ID == room.ID && c.MemberCount != 1 {
				t.Errorf("got member_count %d, want 1 (creator only)", c.MemberCount)
			}
		}
	})

	// Alice stays connected and joined so she can observe bob's self-join
	// broadcast live, same pattern as TestWebSocket_MessageDeliveredToOtherClient.
	aliceConn := dialTestWS(t, srv, alice)
	sendWS(t, aliceConn, map[string]any{"type": "room.join", "room_id": room.ID})
	readUntil(t, aliceConn, "user.joined") // alice's own join

	t.Run("self-join succeeds and broadcasts user.joined", func(t *testing.T) {
		var member struct {
			Role string `json:"role"`
		}
		code := postJSONStatus(t, srv, "/api/v1/rooms/"+room.ID+"/join", bob.token, nil, &member)
		if code != http.StatusCreated {
			t.Fatalf("got status %d, want 201", code)
		}
		if member.Role != "member" {
			t.Errorf("got role %q, want %q", member.Role, "member")
		}

		frame := readUntil(t, aliceConn, "user.joined")
		var event struct {
			User struct {
				ID       string `json:"id"`
				Username string `json:"username"`
			} `json:"user"`
			RoomID string `json:"room_id"`
		}
		if err := json.Unmarshal(frame, &event); err != nil {
			t.Fatalf("decoding user.joined: %v", err)
		}
		if event.User.ID != bob.id {
			t.Errorf("got user.joined for %s, want %s", event.User.ID, bob.id)
		}
		if event.RoomID != room.ID {
			t.Errorf("got room_id %q, want %q", event.RoomID, room.ID)
		}
	})

	t.Run("no longer listed for the now-member", func(t *testing.T) {
		var channels []publicChannel
		code := getJSON(t, srv, "/api/v1/rooms/public", bob.token, &channels)
		if code != http.StatusOK {
			t.Fatalf("got status %d, want 200", code)
		}
		if containsChannelID(channels, room.ID) {
			t.Errorf("got %+v, room should be excluded now that bob is a member", channels)
		}
	})

	t.Run("joining again 409s", func(t *testing.T) {
		code := postJSONStatus(t, srv, "/api/v1/rooms/"+room.ID+"/join", bob.token, nil, nil)
		if code != http.StatusConflict {
			t.Errorf("got status %d, want 409", code)
		}
	})

	t.Run("a private channel is neither listed nor joinable", func(t *testing.T) {
		var privateRoom struct {
			ID string `json:"id"`
		}
		postJSON(t, srv, "/api/v1/rooms", alice.token, map[string]any{"type": "channel", "name": "secret-room", "is_public": false}, &privateRoom)

		var channels []publicChannel
		code := getJSON(t, srv, "/api/v1/rooms/public", bob.token, &channels)
		if code != http.StatusOK {
			t.Fatalf("got status %d, want 200", code)
		}
		if containsChannelID(channels, privateRoom.ID) {
			t.Errorf("private channel must not appear in the public list: %+v", channels)
		}

		code = postJSONStatus(t, srv, "/api/v1/rooms/"+privateRoom.ID+"/join", bob.token, nil, nil)
		if code != http.StatusForbidden {
			t.Errorf("got status %d, want 403", code)
		}
	})

	t.Run("a direct room is not joinable", func(t *testing.T) {
		var dm struct {
			ID string `json:"id"`
		}
		postJSON(t, srv, "/api/v1/rooms", alice.token, map[string]any{"type": "direct", "peer_user_id": bob.id}, &dm)

		carol := signupTestUser(t, srv, "carol_join")
		code := postJSONStatus(t, srv, "/api/v1/rooms/"+dm.ID+"/join", carol.token, nil, nil)
		if code != http.StatusForbidden {
			t.Errorf("got status %d, want 403", code)
		}
	})

	t.Run("malformed room_id is a 400", func(t *testing.T) {
		code := postJSONStatus(t, srv, "/api/v1/rooms/not-a-uuid/join", bob.token, nil, nil)
		if code != http.StatusBadRequest {
			t.Errorf("got status %d, want 400", code)
		}
	})

	t.Run("requires auth", func(t *testing.T) {
		code := postJSONStatus(t, srv, "/api/v1/rooms/"+room.ID+"/join", "", nil, nil)
		if code != http.StatusUnauthorized {
			t.Errorf("got status %d, want 401", code)
		}
	})
}
