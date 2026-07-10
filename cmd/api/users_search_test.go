package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// getJSON issues an authenticated GET and returns the status code, decoding
// the body into out when out is non-nil and the status is 2xx.
func getJSON(t *testing.T, srv *httptest.Server, path, token string, out any) int {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+path, nil)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if out != nil && resp.StatusCode < 300 {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decoding response from %s: %v", path, err)
		}
	}
	return resp.StatusCode
}

type userSummary struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

func usernames(users []userSummary) []string {
	names := make([]string, len(users))
	for i, u := range users {
		names[i] = u.Username
	}
	return names
}

func TestSearchUsersEndpoint(t *testing.T) {
	srv := newTestServer(t)

	alice := signupTestUser(t, srv, "search_alice")
	signupTestUser(t, srv, "search_alan")
	signupTestUser(t, srv, "search_bob")

	t.Run("requires auth", func(t *testing.T) {
		if code := getJSON(t, srv, "/api/v1/users/search?q=search", "", nil); code != http.StatusUnauthorized {
			t.Errorf("got status %d, want 401", code)
		}
	})

	t.Run("prefix match excludes the caller", func(t *testing.T) {
		var got []userSummary
		code := getJSON(t, srv, "/api/v1/users/search?q=search_al", alice.token, &got)
		if code != http.StatusOK {
			t.Fatalf("got status %d, want 200", code)
		}
		// search_alan matches; search_alice is the caller (excluded).
		if names := usernames(got); len(names) != 1 || names[0] != "search_alan" {
			t.Errorf("got %v, want [search_alan]", names)
		}
	})

	t.Run("empty query returns empty list, not an error", func(t *testing.T) {
		var got []userSummary
		code := getJSON(t, srv, "/api/v1/users/search?q=", alice.token, &got)
		if code != http.StatusOK {
			t.Fatalf("got status %d, want 200", code)
		}
		if len(got) != 0 {
			t.Errorf("got %v, want empty", usernames(got))
		}
	})

	// Confirms /users/search resolves to SearchUsers rather than being captured
	// by the /users/{user_id} param route (which would 400 on "search" as a UUID).
	t.Run("static route wins over the {user_id} param route", func(t *testing.T) {
		code := getJSON(t, srv, "/api/v1/users/search?q=search_bob", alice.token, nil)
		if code != http.StatusOK {
			t.Errorf("got status %d, want 200 (route resolved to SearchUsers)", code)
		}
	})

	t.Run("room_id excludes existing members", func(t *testing.T) {
		bob := signupTestUser(t, srv, "search_room_bob")
		room := createTestChannel(t, srv, alice, "search-room")
		inviteTestUser(t, srv, alice, room, bob.id)

		var got []userSummary
		code := getJSON(t, srv, "/api/v1/users/search?q=search_room_bob&room_id="+room, alice.token, &got)
		if code != http.StatusOK {
			t.Fatalf("got status %d, want 200", code)
		}
		if len(got) != 0 {
			t.Errorf("got %v, want empty (bob is already in the room)", usernames(got))
		}

		// Without the room filter, bob is found again.
		code = getJSON(t, srv, "/api/v1/users/search?q=search_room_bob", alice.token, &got)
		if code != http.StatusOK || len(got) != 1 {
			t.Errorf("got status %d / %v, want 200 / [search_room_bob]", code, usernames(got))
		}
	})

	t.Run("bad room_id is a 400", func(t *testing.T) {
		if code := getJSON(t, srv, "/api/v1/users/search?q=search&room_id=not-a-uuid", alice.token, nil); code != http.StatusBadRequest {
			t.Errorf("got status %d, want 400", code)
		}
	})
}
