package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gws "github.com/gorilla/websocket"

	"github.com/mubeendevelops/convoy-chat/internal/config"
	"github.com/mubeendevelops/convoy-chat/internal/testutil"
	"github.com/mubeendevelops/convoy-chat/internal/websocket"
)

const wsReadTimeout = 5 * time.Second

// newTestServer builds a real router (the same newRouter main() uses) and
// real WebSocket Hub/Broker, backed by a fresh testutil store, and serves it
// via httptest — so this test drives the actual production wiring end to
// end, not a stand-in.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	st := testutil.NewStore(t)

	cfg := &config.Config{
		AppEnv:             "test",
		JWTSecret:          "integration-test-secret-32-bytes!!",
		JWTTTL:             time.Hour,
		CORSAllowedOrigins: []string{"http://localhost:3000"},
	}

	logger := newLogger(cfg.AppEnv)
	wsServer := websocket.NewServer(st, cfg.JWTSecret, cfg.CORSAllowedOrigins, logger)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	wsServer.Run(ctx)

	srv := httptest.NewServer(newRouter(cfg, st, wsServer, logger))
	t.Cleanup(srv.Close)
	return srv
}

type testUser struct {
	id    string
	token string
}

func signupTestUser(t *testing.T, srv *httptest.Server, username string) testUser {
	t.Helper()
	body := map[string]any{
		"username": username,
		"email":    username + "@example.com",
		"password": "Passw0rd123",
	}
	var resp struct {
		Token string `json:"token"`
		User  struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	postJSON(t, srv, "/api/v1/auth/signup", "", body, &resp)
	return testUser{id: resp.User.ID, token: resp.Token}
}

func createTestChannel(t *testing.T, srv *httptest.Server, owner testUser, name string) string {
	t.Helper()
	var resp struct {
		ID string `json:"id"`
	}
	postJSON(t, srv, "/api/v1/rooms", owner.token, map[string]any{"type": "channel", "name": name}, &resp)
	return resp.ID
}

func inviteTestUser(t *testing.T, srv *httptest.Server, owner testUser, roomID, userID string) {
	t.Helper()
	postJSON(t, srv, "/api/v1/rooms/"+roomID+"/invite", owner.token, map[string]any{"user_id": userID}, nil)
}

func postJSON(t *testing.T, srv *httptest.Server, path, token string, body any, out any) {
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
	if resp.StatusCode >= 300 {
		t.Fatalf("%s: got status %d", path, resp.StatusCode)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decoding response from %s: %v", path, err)
		}
	}
}

func dialTestWS(t *testing.T, srv *httptest.Server, u testUser) *gws.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws?token=" + u.token
	conn, resp, err := gws.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dialing websocket: %v", err)
	}
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func sendWS(t *testing.T, conn *gws.Conn, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshaling ws message: %v", err)
	}
	if err := conn.WriteMessage(gws.TextMessage, data); err != nil {
		t.Fatalf("writing ws message: %v", err)
	}
}

// readUntil reads frames from conn (bounded by wsReadTimeout total) until it
// finds one whose "type" field equals eventType, and returns its raw bytes.
// Frames of other types (e.g. user.joined, user.status_changed, which this
// test doesn't care about) are skipped rather than failing the test.
func readUntil(t *testing.T, conn *gws.Conn, eventType string) []byte {
	t.Helper()
	deadline := time.Now().Add(wsReadTimeout)
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatalf("setting read deadline: %v", err)
		}
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("waiting for a %q event: %v", eventType, err)
		}
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(data, &envelope); err != nil {
			t.Fatalf("decoding ws frame: %v (frame=%s)", err, data)
		}
		if envelope.Type == eventType {
			return data
		}
	}
}

// TestWebSocket_MessageDeliveredToOtherClient is the Phase 8 WS integration
// test: two clients connect, join the same room, one sends a message, and
// the other must receive message.new for it.
func TestWebSocket_MessageDeliveredToOtherClient(t *testing.T) {
	srv := newTestServer(t)

	alice := signupTestUser(t, srv, "alice_ws")
	bob := signupTestUser(t, srv, "bob_ws")
	room := createTestChannel(t, srv, alice, "integration-room")
	inviteTestUser(t, srv, alice, room, bob.id)

	aliceConn := dialTestWS(t, srv, alice)
	bobConn := dialTestWS(t, srv, bob)

	sendWS(t, aliceConn, map[string]any{"type": "room.join", "room_id": room})
	sendWS(t, bobConn, map[string]any{"type": "room.join", "room_id": room})

	// Drain each client's own user.joined/user.status_changed noise from
	// joining, so it doesn't interfere with the two message.new waits below.
	readUntil(t, aliceConn, "user.joined")
	readUntil(t, bobConn, "user.joined")

	const content = "hello from the integration test"
	sendWS(t, aliceConn, map[string]any{"type": "message.send", "room_id": room, "content": content})

	frame := readUntil(t, bobConn, "message.new")
	var received struct {
		Message struct {
			Content *string `json:"content"`
			RoomID  string  `json:"room_id"`
		} `json:"message"`
	}
	if err := json.Unmarshal(frame, &received); err != nil {
		t.Fatalf("decoding message.new: %v", err)
	}
	if received.Message.Content == nil || *received.Message.Content != content {
		t.Errorf("got content %v, want %q", received.Message.Content, content)
	}
	if received.Message.RoomID != room {
		t.Errorf("got room_id %q, want %q", received.Message.RoomID, room)
	}
}
