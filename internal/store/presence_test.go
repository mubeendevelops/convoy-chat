package store_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mubeendevelops/convoy-chat/internal/models"
	"github.com/mubeendevelops/convoy-chat/internal/testutil"
)

// presenceStatusKey mirrors the unexported key format in store/presence.go
// (package store_test can't reach the unexported helper directly, and
// store.Store.Redis is exported specifically so callers/tests can do exactly
// this kind of direct inspection).
func presenceStatusKey(userID uuid.UUID) string { return "presence:status:" + userID.String() }

func TestPresenceConnect_FirstConnectionGoesOnline(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	userID := mustCreateUser(t, s, "presence_a")

	wentOnline, err := s.PresenceConnect(ctx, userID, time.Minute)
	if err != nil {
		t.Fatalf("PresenceConnect: %v", err)
	}
	if !wentOnline {
		t.Error("expected the first connection to report wentOnline=true")
	}

	val, err := s.Redis.Get(ctx, presenceStatusKey(userID)).Result()
	if err != nil {
		t.Fatalf("reading presence status key: %v", err)
	}
	if val != string(models.PresenceOnline) {
		t.Errorf("got status %q, want %q", val, models.PresenceOnline)
	}
}

func TestPresenceConnect_SecondConnectionIsSilent(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	userID := mustCreateUser(t, s, "presence_b")

	if _, err := s.PresenceConnect(ctx, userID, time.Minute); err != nil {
		t.Fatalf("PresenceConnect (first): %v", err)
	}

	wentOnline, err := s.PresenceConnect(ctx, userID, time.Minute)
	if err != nil {
		t.Fatalf("PresenceConnect (second): %v", err)
	}
	if wentOnline {
		t.Error("expected a second connection (another tab) to report wentOnline=false")
	}
}

func TestPresenceDisconnect_PartialDisconnectStaysOnline(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	userID := mustCreateUser(t, s, "presence_c")

	if _, err := s.PresenceConnect(ctx, userID, time.Minute); err != nil {
		t.Fatalf("PresenceConnect (tab 1): %v", err)
	}
	if _, err := s.PresenceConnect(ctx, userID, time.Minute); err != nil {
		t.Fatalf("PresenceConnect (tab 2): %v", err)
	}

	wentOffline, err := s.PresenceDisconnect(ctx, userID)
	if err != nil {
		t.Fatalf("PresenceDisconnect (tab 1 closes): %v", err)
	}
	if wentOffline {
		t.Error("expected closing one of two connections to report wentOffline=false")
	}

	exists, err := s.Redis.Exists(ctx, presenceStatusKey(userID)).Result()
	if err != nil {
		t.Fatalf("checking presence status key: %v", err)
	}
	if exists == 0 {
		t.Error("expected the status key to still exist — the user has another live connection")
	}
}

func TestPresenceDisconnect_LastConnectionGoesOffline(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	userID := mustCreateUser(t, s, "presence_d")

	if _, err := s.PresenceConnect(ctx, userID, time.Minute); err != nil {
		t.Fatalf("PresenceConnect: %v", err)
	}

	wentOffline, err := s.PresenceDisconnect(ctx, userID)
	if err != nil {
		t.Fatalf("PresenceDisconnect: %v", err)
	}
	if !wentOffline {
		t.Error("expected closing the last connection to report wentOffline=true")
	}

	exists, err := s.Redis.Exists(ctx, presenceStatusKey(userID)).Result()
	if err != nil {
		t.Fatalf("checking presence status key: %v", err)
	}
	if exists != 0 {
		t.Error("expected the status key to be gone once the last connection closes")
	}
}

func TestPresenceDisconnect_WithoutAPriorConnectDoesNotGoNegative(t *testing.T) {
	// Regression guard for the crash-drift case store/presence.go documents:
	// a disconnect with no matching prior connect (e.g. a leaked decrement
	// after a crash) must clamp at 0, not go negative and confuse the next
	// real connect's 0→1 detection.
	s := testutil.NewStore(t)
	ctx := t.Context()
	userID := mustCreateUser(t, s, "presence_e")

	if _, err := s.PresenceDisconnect(ctx, userID); err != nil {
		t.Fatalf("PresenceDisconnect (no prior connect): %v", err)
	}

	wentOnline, err := s.PresenceConnect(ctx, userID, time.Minute)
	if err != nil {
		t.Fatalf("PresenceConnect (after the stray disconnect): %v", err)
	}
	if !wentOnline {
		t.Error("expected a fresh connect after a stray disconnect to still be detected as 0→1 (wentOnline=true)")
	}
}

func TestPresenceSetStatus_SurvivesNewConnection(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	userID := mustCreateUser(t, s, "presence_f")

	if _, err := s.PresenceConnect(ctx, userID, time.Minute); err != nil {
		t.Fatalf("PresenceConnect: %v", err)
	}
	if err := s.PresenceSetStatus(ctx, userID, models.PresenceAway, time.Minute); err != nil {
		t.Fatalf("PresenceSetStatus(away): %v", err)
	}

	// A second tab connecting must not silently reset "away" back to
	// "online" — only a fresh 0→1 connect (a genuinely new session) does.
	if _, err := s.PresenceConnect(ctx, userID, time.Minute); err != nil {
		t.Fatalf("PresenceConnect (second tab): %v", err)
	}

	val, err := s.Redis.Get(ctx, presenceStatusKey(userID)).Result()
	if err != nil {
		t.Fatalf("reading presence status key: %v", err)
	}
	if val != string(models.PresenceAway) {
		t.Errorf("got status %q after a second tab connected, want %q (unchanged)", val, models.PresenceAway)
	}
}

func TestPresenceHeartbeat_RefreshesTTL(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	userID := mustCreateUser(t, s, "presence_g")

	const shortTTL = 300 * time.Millisecond
	if _, err := s.PresenceConnect(ctx, userID, shortTTL); err != nil {
		t.Fatalf("PresenceConnect: %v", err)
	}

	// Heartbeat partway through the TTL window, twice — each beat should
	// push the deadline back out, so the key survives well past the
	// original TTL as long as beats keep arriving on time.
	for range 2 {
		time.Sleep(shortTTL / 2)
		if err := s.PresenceHeartbeat(ctx, userID, shortTTL); err != nil {
			t.Fatalf("PresenceHeartbeat: %v", err)
		}
	}

	exists, err := s.Redis.Exists(ctx, presenceStatusKey(userID)).Result()
	if err != nil {
		t.Fatalf("checking presence status key: %v", err)
	}
	if exists == 0 {
		t.Error("expected the status key to still exist — heartbeats kept refreshing it before each expiry")
	}
}

func TestListAllUserPresence(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	online := mustCreateUser(t, s, "presence_list_online")
	offline := mustCreateUser(t, s, "presence_list_offline")

	if _, err := s.PresenceConnect(ctx, online, time.Minute); err != nil {
		t.Fatalf("PresenceConnect: %v", err)
	}
	// offline never connects — must still appear, defaulted to offline.

	entries, err := s.ListAllUserPresence(ctx)
	if err != nil {
		t.Fatalf("ListAllUserPresence: %v", err)
	}

	byID := make(map[uuid.UUID]models.AdminPresenceEntry, len(entries))
	for _, e := range entries {
		byID[e.UserID] = e
	}

	onlineEntry, ok := byID[online]
	if !ok {
		t.Fatal("expected the connected user to appear in the snapshot")
	}
	if onlineEntry.Status != models.PresenceOnline {
		t.Errorf("got status %q for the connected user, want %q", onlineEntry.Status, models.PresenceOnline)
	}

	offlineEntry, ok := byID[offline]
	if !ok {
		t.Fatal("expected the never-connected user to appear in the snapshot")
	}
	if offlineEntry.Status != models.PresenceOffline {
		t.Errorf("got status %q for a user with no live Redis entry, want %q (default)", offlineEntry.Status, models.PresenceOffline)
	}
}

func TestPresenceHeartbeat_ResurrectsLapsedKey(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	userID := mustCreateUser(t, s, "presence_h")

	const shortTTL = 100 * time.Millisecond
	if _, err := s.PresenceConnect(ctx, userID, shortTTL); err != nil {
		t.Fatalf("PresenceConnect: %v", err)
	}

	// Let the key actually lapse (simulates a heartbeat that arrived too
	// late — e.g. a long scheduling delay) and confirm it's really gone
	// before testing the resurrect path, so this test would fail loudly if
	// the TTL isn't behaving as expected rather than passing vacuously.
	time.Sleep(shortTTL * 3)
	existsBefore, err := s.Redis.Exists(ctx, presenceStatusKey(userID)).Result()
	if err != nil {
		t.Fatalf("checking presence status key: %v", err)
	}
	if existsBefore != 0 {
		t.Fatal("expected the status key to have already expired before the resurrect heartbeat")
	}

	if err := s.PresenceHeartbeat(ctx, userID, time.Minute); err != nil {
		t.Fatalf("PresenceHeartbeat (resurrecting): %v", err)
	}

	val, err := s.Redis.Get(ctx, presenceStatusKey(userID)).Result()
	if err != nil {
		t.Fatalf("reading presence status key after resurrect: %v", err)
	}
	if val != string(models.PresenceOnline) {
		t.Errorf("got status %q after resurrection, want %q", val, models.PresenceOnline)
	}
}
