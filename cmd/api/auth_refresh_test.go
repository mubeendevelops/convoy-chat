package main

import (
	"net/http/httptest"
	"testing"
)

// authTriple mirrors internal/handlers.authResponse — signup/login/refresh
// all return this same shape.
type authTriple struct {
	Token        string `json:"token"`
	RefreshToken string `json:"refresh_token"`
	User         struct {
		ID string `json:"id"`
	} `json:"user"`
}

func signupWithRefresh(t *testing.T, srv *httptest.Server, username string) authTriple {
	t.Helper()
	body := map[string]any{
		"username": username,
		"email":    username + "@example.com",
		"password": "Passw0rd123",
	}
	var resp authTriple
	postJSON(t, srv, "/api/v1/auth/signup", "", body, &resp)
	if resp.RefreshToken == "" {
		t.Fatal("signup response carried no refresh_token")
	}
	return resp
}

// TestAuthRefresh_RotationHappyPath drives signup -> refresh -> refresh
// again, asserting each rotation issues a genuinely new access+refresh pair
// and that the newest access token is actually usable against a protected
// endpoint.
func TestAuthRefresh_RotationHappyPath(t *testing.T) {
	srv := newTestServer(t)
	triple1 := signupWithRefresh(t, srv, "rotator1")

	var triple2 authTriple
	postJSON(t, srv, "/api/v1/auth/refresh", "", map[string]any{"refresh_token": triple1.RefreshToken}, &triple2)
	// Not asserting triple2.Token != triple1.Token: a JWT's claims (including
	// iat, second-precision) can be identical to the previous call if both
	// land in the same second, making the signed token byte-identical too —
	// that's expected determinism, not a bug. The refresh token is what must
	// always differ, since it's freshly random on every rotation.
	if triple2.RefreshToken == triple1.RefreshToken {
		t.Error("refresh returned the same refresh token as before")
	}
	if triple2.User.ID != triple1.User.ID {
		t.Errorf("refresh returned user %s, want %s", triple2.User.ID, triple1.User.ID)
	}

	// The new access token must actually work.
	if status := postJSONStatus(t, srv, "/api/v1/rooms", triple2.Token, map[string]any{"type": "channel", "name": "rotation-check"}, nil); status != 201 {
		t.Errorf("using the newly-rotated access token: got status %d, want 201", status)
	}

	// A second rotation, chained off the first, must also succeed.
	var triple3 authTriple
	postJSON(t, srv, "/api/v1/auth/refresh", "", map[string]any{"refresh_token": triple2.RefreshToken}, &triple3)
	if triple3.RefreshToken == triple2.RefreshToken {
		t.Error("second refresh returned the same refresh token as before")
	}
}

// TestAuthRefresh_ReplayedTokenRejected: presenting an already-rotated-out
// refresh token 401s rather than succeeding.
func TestAuthRefresh_ReplayedTokenRejected(t *testing.T) {
	srv := newTestServer(t)
	triple1 := signupWithRefresh(t, srv, "replayer")

	var triple2 authTriple
	postJSON(t, srv, "/api/v1/auth/refresh", "", map[string]any{"refresh_token": triple1.RefreshToken}, &triple2)

	if status := postJSONStatus(t, srv, "/api/v1/auth/refresh", "", map[string]any{"refresh_token": triple1.RefreshToken}, nil); status != 401 {
		t.Errorf("replaying an already-rotated-out refresh token: got status %d, want 401", status)
	}
}

// TestAuthRefresh_ReuseRevokesWholeFamily is the security property the
// rotation design exists for: replaying an old token doesn't just fail that
// one request, it also kills the legitimate client's newer token in the same
// family — an attacker who stole an old refresh token and a legitimate user
// racing to rotate first both end up locked out, forcing a fresh login.
func TestAuthRefresh_ReuseRevokesWholeFamily(t *testing.T) {
	srv := newTestServer(t)
	triple1 := signupWithRefresh(t, srv, "victim")

	var triple2 authTriple
	postJSON(t, srv, "/api/v1/auth/refresh", "", map[string]any{"refresh_token": triple1.RefreshToken}, &triple2)

	// Replay the old (stolen) token — this trips reuse detection.
	if status := postJSONStatus(t, srv, "/api/v1/auth/refresh", "", map[string]any{"refresh_token": triple1.RefreshToken}, nil); status != 401 {
		t.Fatalf("replay: got status %d, want 401", status)
	}

	// The legitimate holder's still-current triple2 refresh token must now
	// also be dead, even though it was never itself replayed.
	if status := postJSONStatus(t, srv, "/api/v1/auth/refresh", "", map[string]any{"refresh_token": triple2.RefreshToken}, nil); status != 401 {
		t.Errorf("refreshing with the legitimate (pre-replay) token after family revocation: got status %d, want 401", status)
	}
}

// TestAuthRefresh_GarbageAndEmptyToken: malformed input 400s, a well-formed
// but unknown token 401s — neither should 500.
func TestAuthRefresh_GarbageAndEmptyToken(t *testing.T) {
	srv := newTestServer(t)

	if status := postJSONStatus(t, srv, "/api/v1/auth/refresh", "", map[string]any{"refresh_token": ""}, nil); status != 400 {
		t.Errorf("empty refresh_token: got status %d, want 400", status)
	}
	if status := postJSONStatus(t, srv, "/api/v1/auth/refresh", "", map[string]any{"refresh_token": "not-a-real-token"}, nil); status != 401 {
		t.Errorf("bogus refresh_token: got status %d, want 401", status)
	}
}

// TestAuthLogout_RevokesRefreshToken: logout kills the presented token's
// family, so a subsequent refresh with it 401s.
func TestAuthLogout_RevokesRefreshToken(t *testing.T) {
	srv := newTestServer(t)
	triple := signupWithRefresh(t, srv, "logout-user")

	var out map[string]string
	postJSON(t, srv, "/api/v1/auth/logout", triple.Token, map[string]any{"refresh_token": triple.RefreshToken}, &out)
	if out["status"] != "logged_out" {
		t.Errorf(`logout response status = %q, want "logged_out"`, out["status"])
	}

	if status := postJSONStatus(t, srv, "/api/v1/auth/refresh", "", map[string]any{"refresh_token": triple.RefreshToken}, nil); status != 401 {
		t.Errorf("refreshing with a logged-out token: got status %d, want 401", status)
	}
}

// TestAuthLogout_MissingTokenIsNoop: logging out without a refresh_token
// (or with one that doesn't exist) is a no-op 200, not an error.
func TestAuthLogout_MissingTokenIsNoop(t *testing.T) {
	srv := newTestServer(t)
	triple := signupWithRefresh(t, srv, "noop-logout")

	if status := postJSONStatus(t, srv, "/api/v1/auth/logout", triple.Token, map[string]any{}, nil); status != 200 {
		t.Errorf("logout with no refresh_token: got status %d, want 200", status)
	}
	if status := postJSONStatus(t, srv, "/api/v1/auth/logout", triple.Token, map[string]any{"refresh_token": "does-not-exist"}, nil); status != 200 {
		t.Errorf("logout with an unknown refresh_token: got status %d, want 200", status)
	}
}

// TestAuthLogout_RequiresAuth: unlike refresh, logout sits behind the
// Bearer-auth middleware.
func TestAuthLogout_RequiresAuth(t *testing.T) {
	srv := newTestServer(t)

	if status := postJSONStatus(t, srv, "/api/v1/auth/logout", "", map[string]any{"refresh_token": "whatever"}, nil); status != 401 {
		t.Errorf("logout with no Bearer token: got status %d, want 401", status)
	}
}
