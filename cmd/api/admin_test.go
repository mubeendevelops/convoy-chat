package main

import (
	"net/http"
	"testing"
)

// Tests below call st.PromoteToSystemAdmin directly — no REST endpoint
// exists for this by design (see plan.md's admin-dashboard proposal), so
// tests reach the store the same way the `-promote-admin` CLI mode does.
// Mirrors signupTestUser's username+"@example.com" email convention.

type adminRoomSummary struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	MemberCount int    `json:"member_count"`
	Creator     struct {
		ID string `json:"id"`
	} `json:"creator"`
}

type adminPresenceEntry struct {
	UserID string `json:"user_id"`
	Status string `json:"status"`
}

func TestRequireSystemAdmin_ForbidsNonAdmin(t *testing.T) {
	srv, _ := newTestServerWithStore(t)
	alice := signupTestUser(t, srv, "sysadmin_forbid_alice")

	if status := getJSON(t, srv, "/api/v1/admin/rooms", alice.token, nil); status != http.StatusForbidden {
		t.Errorf("non-admin GET /admin/rooms: got status %d, want 403", status)
	}
	if status := getJSON(t, srv, "/api/v1/admin/presence", alice.token, nil); status != http.StatusForbidden {
		t.Errorf("non-admin GET /admin/presence: got status %d, want 403", status)
	}
}

func TestAdminRooms_ListsEveryRoomRegardlessOfMembership(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	alice := signupTestUser(t, srv, "sysadmin_rooms_alice")
	bob := signupTestUser(t, srv, "sysadmin_rooms_bob")

	if _, err := st.PromoteToSystemAdmin(t.Context(), "sysadmin_rooms_bob@example.com"); err != nil {
		t.Fatalf("PromoteToSystemAdmin: %v", err)
	}

	roomID := createTestChannel(t, srv, alice, "sysadmin-visibility-room")

	var rooms []adminRoomSummary
	status := getJSON(t, srv, "/api/v1/admin/rooms", bob.token, &rooms)
	if status != http.StatusOK {
		t.Fatalf("admin GET /admin/rooms: got status %d, want 200", status)
	}

	found := false
	for _, r := range rooms {
		if r.ID == roomID {
			found = true
			if r.MemberCount != 1 {
				t.Errorf("got member_count %d, want 1 (bob never joined this room)", r.MemberCount)
			}
			if r.Creator.ID != alice.id {
				t.Errorf("got creator %s, want %s", r.Creator.ID, alice.id)
			}
		}
	}
	if !found {
		t.Error("expected a room bob never joined to still appear in the admin listing")
	}
}

func TestAdminRooms_PaginationValidation(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	alice := signupTestUser(t, srv, "sysadmin_paging_alice")
	if _, err := st.PromoteToSystemAdmin(t.Context(), "sysadmin_paging_alice@example.com"); err != nil {
		t.Fatalf("PromoteToSystemAdmin: %v", err)
	}

	if status := getJSON(t, srv, "/api/v1/admin/rooms?limit=0", alice.token, nil); status != http.StatusBadRequest {
		t.Errorf("limit=0: got status %d, want 400", status)
	}
	if status := getJSON(t, srv, "/api/v1/admin/rooms?limit=201", alice.token, nil); status != http.StatusBadRequest {
		t.Errorf("limit=201: got status %d, want 400", status)
	}
	if status := getJSON(t, srv, "/api/v1/admin/rooms?offset=-1", alice.token, nil); status != http.StatusBadRequest {
		t.Errorf("offset=-1: got status %d, want 400", status)
	}
	if status := getJSON(t, srv, "/api/v1/admin/rooms?limit=10&offset=0", alice.token, nil); status != http.StatusOK {
		t.Errorf("valid paging: got status %d, want 200", status)
	}
}

func TestAdminPresence_IncludesOfflineUsers(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	admin := signupTestUser(t, srv, "sysadmin_presence_admin")
	_ = signupTestUser(t, srv, "sysadmin_presence_offline") // never connects over WS
	if _, err := st.PromoteToSystemAdmin(t.Context(), "sysadmin_presence_admin@example.com"); err != nil {
		t.Fatalf("PromoteToSystemAdmin: %v", err)
	}

	var entries []adminPresenceEntry
	status := getJSON(t, srv, "/api/v1/admin/presence", admin.token, &entries)
	if status != http.StatusOK {
		t.Fatalf("admin GET /admin/presence: got status %d, want 200", status)
	}

	byID := make(map[string]string, len(entries))
	for _, e := range entries {
		byID[e.UserID] = e.Status
	}
	if status, ok := byID[admin.id]; !ok || status == "" {
		t.Error("expected the admin's own user to appear in the presence snapshot")
	}
}

// TestAdminBypass_RoomDetailAndMembers confirms a system admin who is NOT a
// member of a room can still fetch its detail/members via the existing
// (widened, not new) endpoints, while a non-admin non-member still 403s.
func TestAdminBypass_RoomDetailAndMembers(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	alice := signupTestUser(t, srv, "sysadmin_bypass_alice")
	admin := signupTestUser(t, srv, "sysadmin_bypass_admin")
	outsider := signupTestUser(t, srv, "sysadmin_bypass_outsider")
	if _, err := st.PromoteToSystemAdmin(t.Context(), "sysadmin_bypass_admin@example.com"); err != nil {
		t.Fatalf("PromoteToSystemAdmin: %v", err)
	}

	roomID := createTestChannel(t, srv, alice, "sysadmin-bypass-room")

	if status := getJSON(t, srv, "/api/v1/rooms/"+roomID, admin.token, nil); status != http.StatusOK {
		t.Errorf("system admin (non-member) GET room detail: got status %d, want 200", status)
	}
	if status := getJSON(t, srv, "/api/v1/rooms/"+roomID+"/members", admin.token, nil); status != http.StatusOK {
		t.Errorf("system admin (non-member) GET members: got status %d, want 200", status)
	}

	if status := getJSON(t, srv, "/api/v1/rooms/"+roomID, outsider.token, nil); status != http.StatusForbidden {
		t.Errorf("plain non-member GET room detail: got status %d, want 403 (unwidened)", status)
	}
}

// TestAdminBypass_DeleteAnyMessage confirms "message moderation": a system
// admin can delete a message in a room they don't belong to.
func TestAdminBypass_DeleteAnyMessage(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	alice := signupTestUser(t, srv, "sysadmin_delete_alice")
	admin := signupTestUser(t, srv, "sysadmin_delete_admin")
	if _, err := st.PromoteToSystemAdmin(t.Context(), "sysadmin_delete_admin@example.com"); err != nil {
		t.Fatalf("PromoteToSystemAdmin: %v", err)
	}

	roomID := createTestChannel(t, srv, alice, "sysadmin-delete-room")
	var sendResp struct {
		ID string `json:"id"`
	}
	postJSON(t, srv, "/api/v1/rooms/"+roomID+"/messages", alice.token, map[string]any{"content": "delete me"}, &sendResp)

	if status := deleteJSONStatus(t, srv, "/api/v1/messages/"+sendResp.ID, admin.token); status != http.StatusOK {
		t.Errorf("system admin (non-member) deleting a message: got status %d, want 200", status)
	}
}

// TestAdminScopeBoundary_NoRoomMembershipPower is the regression guard the
// proposal explicitly called for: a system admin who isn't a room admin must
// still be rejected by InviteMember/ChangeMemberRole/RemoveMember — system-
// admin power deliberately doesn't extend to membership management in rooms
// they don't belong to.
func TestAdminScopeBoundary_NoRoomMembershipPower(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	alice := signupTestUser(t, srv, "sysadmin_scope_alice")
	admin := signupTestUser(t, srv, "sysadmin_scope_admin")
	bob := signupTestUser(t, srv, "sysadmin_scope_bob")
	if _, err := st.PromoteToSystemAdmin(t.Context(), "sysadmin_scope_admin@example.com"); err != nil {
		t.Fatalf("PromoteToSystemAdmin: %v", err)
	}

	roomID := createTestChannel(t, srv, alice, "sysadmin-scope-room")
	inviteTestUser(t, srv, alice, roomID, bob.id)

	if status := postJSONStatus(t, srv, "/api/v1/rooms/"+roomID+"/invite", admin.token, map[string]any{"user_id": admin.id}, nil); status != http.StatusForbidden {
		t.Errorf("system admin (non-room-admin) invite: got status %d, want 403", status)
	}
	if status := patchJSONStatus(t, srv, "/api/v1/rooms/"+roomID+"/members/"+bob.id+"/role", admin.token, map[string]any{"role": "admin"}, nil); status != http.StatusForbidden {
		t.Errorf("system admin (non-room-admin) change-role: got status %d, want 403", status)
	}
	if status := deleteJSONStatus(t, srv, "/api/v1/rooms/"+roomID+"/members/"+bob.id, admin.token); status != http.StatusForbidden {
		t.Errorf("system admin (non-room-admin) kick: got status %d, want 403", status)
	}
}
