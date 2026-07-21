package store_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mubeendevelops/convoy-chat/internal/models"
	"github.com/mubeendevelops/convoy-chat/internal/store"
	"github.com/mubeendevelops/convoy-chat/internal/testutil"
)

// mustCreateUser is a small fixture helper: rooms/membership rows have a
// foreign key on users, so most of these tests need a handful of real users.
func mustCreateUser(t *testing.T, s *store.Store, username string) uuid.UUID {
	t.Helper()
	user, err := s.CreateUser(t.Context(), username, username+"@example.com", "hash")
	if err != nil {
		t.Fatalf("creating fixture user %q: %v", username, err)
	}
	return user.ID
}

func TestCreateChannel(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	creator := mustCreateUser(t, s, "creator")

	desc := "a test channel"
	room, err := s.CreateChannel(ctx, creator, "general", &desc, true)
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	if room.Type != models.RoomTypeChannel {
		t.Errorf("got type %q, want %q", room.Type, models.RoomTypeChannel)
	}

	// The creator must be an admin member.
	membership, err := s.GetMembership(ctx, room.ID, creator)
	if err != nil {
		t.Fatalf("GetMembership for creator: %v", err)
	}
	if membership.Role != models.RoleAdmin {
		t.Errorf("got role %q, want %q", membership.Role, models.RoleAdmin)
	}
}

func TestGetOrCreateDirectRoom(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	alice := mustCreateUser(t, s, "alice")
	bob := mustCreateUser(t, s, "bob")

	room1, created1, peerReactivated1, err := s.GetOrCreateDirectRoom(ctx, alice, bob)
	if err != nil {
		t.Fatalf("GetOrCreateDirectRoom (first call): %v", err)
	}
	if !created1 {
		t.Error("expected created=true on the first call")
	}
	if peerReactivated1 {
		t.Error("expected peerReactivated=false on the first call (nothing to reactivate)")
	}
	if room1.Type != models.RoomTypeDirect {
		t.Errorf("got type %q, want %q", room1.Type, models.RoomTypeDirect)
	}

	t.Run("second call from the same caller dedupes", func(t *testing.T) {
		room2, created2, peerReactivated2, err := s.GetOrCreateDirectRoom(ctx, alice, bob)
		if err != nil {
			t.Fatalf("GetOrCreateDirectRoom: %v", err)
		}
		if created2 {
			t.Error("expected created=false on the second call")
		}
		if peerReactivated2 {
			t.Error("expected peerReactivated=false: neither side had left")
		}
		if room2.ID != room1.ID {
			t.Errorf("got a different room id %s, want %s", room2.ID, room1.ID)
		}
	})

	t.Run("reversed argument order dedupes to the same room", func(t *testing.T) {
		room3, created3, peerReactivated3, err := s.GetOrCreateDirectRoom(ctx, bob, alice)
		if err != nil {
			t.Fatalf("GetOrCreateDirectRoom: %v", err)
		}
		if created3 {
			t.Error("expected created=false when the peer calls back")
		}
		if peerReactivated3 {
			t.Error("expected peerReactivated=false: neither side had left")
		}
		if room3.ID != room1.ID {
			t.Errorf("got a different room id %s, want %s", room3.ID, room1.ID)
		}
	})
}

// TestGetOrCreateDirectRoom_ReactivatesAfterCallerLeft is a regression test
// for the "leaving a DM orphans it" bug: once the caller (userA) has left
// the direct room, calling GetOrCreateDirectRoom for the same pair again
// must resume the SAME room (not fork a new one) and restore the caller's
// own active membership — not just report created=false while leaving them
// permanently absent from the room GetOrCreateDirectRoom itself returns.
func TestGetOrCreateDirectRoom_ReactivatesAfterCallerLeft(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	alice := mustCreateUser(t, s, "reactivate_alice")
	bob := mustCreateUser(t, s, "reactivate_bob")

	original, _, _, err := s.GetOrCreateDirectRoom(ctx, alice, bob)
	if err != nil {
		t.Fatalf("GetOrCreateDirectRoom (initial): %v", err)
	}

	if err := s.RemoveMember(ctx, original.ID, alice); err != nil {
		t.Fatalf("alice leaving: %v", err)
	}
	if _, err := s.GetMembership(ctx, original.ID, alice); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("alice's membership after leaving: got err %v, want ErrNotFound", err)
	}
	// Bob's own membership must be completely untouched by alice leaving.
	if _, err := s.GetMembership(ctx, original.ID, bob); err != nil {
		t.Fatalf("bob's membership should be unaffected by alice leaving: %v", err)
	}

	again, created, peerReactivated, err := s.GetOrCreateDirectRoom(ctx, alice, bob)
	if err != nil {
		t.Fatalf("GetOrCreateDirectRoom (after alice left): %v", err)
	}
	if created {
		t.Error("expected created=false — the room already existed, it should be resumed, not forked")
	}
	if peerReactivated {
		t.Error("expected peerReactivated=false — bob (the peer/userB) never left, alice (the caller) did")
	}
	if again.ID != original.ID {
		t.Errorf("got a different room id %s, want the original %s — leaving must not fork a duplicate DM", again.ID, original.ID)
	}

	if _, err := s.GetMembership(ctx, original.ID, alice); err != nil {
		t.Errorf("alice should be an active member again after resuming: %v", err)
	}
}

// TestGetOrCreateDirectRoom_ReactivatesAndFlagsPeer covers the mirror image:
// the *peer* (userB) had left, and the still-present caller (userA) starts
// the conversation again. The peer's own membership must be silently
// restored server-side, and peerReactivated must come back true so the
// handler knows to give the peer a live nudge — their own client has no
// other way to learn they're back in a room they thought they'd left.
func TestGetOrCreateDirectRoom_ReactivatesAndFlagsPeer(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	alice := mustCreateUser(t, s, "reactivate_peer_alice")
	bob := mustCreateUser(t, s, "reactivate_peer_bob")

	original, _, _, err := s.GetOrCreateDirectRoom(ctx, alice, bob)
	if err != nil {
		t.Fatalf("GetOrCreateDirectRoom (initial): %v", err)
	}
	if err := s.RemoveMember(ctx, original.ID, bob); err != nil {
		t.Fatalf("bob leaving: %v", err)
	}

	again, created, peerReactivated, err := s.GetOrCreateDirectRoom(ctx, alice, bob)
	if err != nil {
		t.Fatalf("GetOrCreateDirectRoom (after bob left): %v", err)
	}
	if created {
		t.Error("expected created=false — the room already existed")
	}
	if !peerReactivated {
		t.Error("expected peerReactivated=true — bob (userB/the peer) was the one who left")
	}
	if again.ID != original.ID {
		t.Errorf("got a different room id %s, want the original %s", again.ID, original.ID)
	}
	if _, err := s.GetMembership(ctx, original.ID, bob); err != nil {
		t.Errorf("bob should be an active member again: %v", err)
	}
}

// TestGetOrCreateDirectRoom_ConcurrentDedup is a regression test for the
// advisory-lock-based dedup verified manually in Phase 3: 10 simultaneous
// creation requests for a brand-new pair must resolve to exactly one room.
func TestGetOrCreateDirectRoom_ConcurrentDedup(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	dave := mustCreateUser(t, s, "dave")
	erin := mustCreateUser(t, s, "erin")

	const attempts = 10
	roomIDs := make([]uuid.UUID, attempts)
	createdFlags := make([]bool, attempts)
	errs := make([]error, attempts)

	var wg sync.WaitGroup
	wg.Add(attempts)
	for i := range attempts {
		go func(i int) {
			defer wg.Done()
			room, created, _, err := s.GetOrCreateDirectRoom(ctx, dave, erin)
			if err == nil {
				roomIDs[i] = room.ID
				createdFlags[i] = created
			}
			errs[i] = err
		}(i)
	}
	wg.Wait()

	createdCount := 0
	for i := range attempts {
		if errs[i] != nil {
			t.Fatalf("attempt %d: unexpected error: %v", i, errs[i])
		}
		if roomIDs[i] != roomIDs[0] {
			t.Errorf("attempt %d resolved to room %s, want %s (all attempts must agree)", i, roomIDs[i], roomIDs[0])
		}
		if createdFlags[i] {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Errorf("got %d attempts reporting created=true, want exactly 1", createdCount)
	}

	// And confirm at the database level too: exactly one direct room exists
	// for this pair, not just that every goroutine agreed on an ID.
	rooms, err := s.ListRoomsForUser(ctx, dave)
	if err != nil {
		t.Fatalf("ListRoomsForUser: %v", err)
	}
	directCount := 0
	for _, r := range rooms {
		if r.Type == models.RoomTypeDirect {
			directCount++
		}
	}
	if directCount != 1 {
		t.Errorf("got %d direct rooms for dave, want 1", directCount)
	}
}

func TestAddMember_RemoveMember_Reactivation(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	creator := mustCreateUser(t, s, "creator")
	member := mustCreateUser(t, s, "member")

	room, err := s.CreateChannel(ctx, creator, "team", nil, true)
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}

	added, err := s.AddMember(ctx, room.ID, member, models.RoleMember)
	if err != nil {
		t.Fatalf("AddMember: %v", err)
	}
	firstJoinedAt := added.JoinedAt

	t.Run("already-active member is rejected", func(t *testing.T) {
		_, err := s.AddMember(ctx, room.ID, member, models.RoleMember)
		if !errors.Is(err, store.ErrAlreadyMember) {
			t.Errorf("got error %v, want ErrAlreadyMember", err)
		}
	})

	t.Run("leaving then re-inviting reactivates rather than erroring", func(t *testing.T) {
		if err := s.RemoveMember(ctx, room.ID, member); err != nil {
			t.Fatalf("RemoveMember: %v", err)
		}

		// While departed, membership must read as not-found.
		if _, err := s.GetMembership(ctx, room.ID, member); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("GetMembership after leaving: got %v, want ErrNotFound", err)
		}

		reactivated, err := s.AddMember(ctx, room.ID, member, models.RoleMember)
		if err != nil {
			t.Fatalf("re-inviting a departed member: %v", err)
		}
		if reactivated.ID != added.ID {
			t.Errorf("expected the same membership row id to be reused (id=%s), got a new one (id=%s)", added.ID, reactivated.ID)
		}
		if reactivated.LeftAt != nil {
			t.Error("expected left_at to be cleared on reactivation")
		}
		if !reactivated.JoinedAt.After(firstJoinedAt) {
			t.Error("expected a fresh joined_at on reactivation")
		}
	})

	t.Run("RemoveMember on a non-member 404s", func(t *testing.T) {
		stranger := mustCreateUser(t, s, "stranger")
		if err := s.RemoveMember(ctx, room.ID, stranger); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("got error %v, want ErrNotFound", err)
		}
	})
}

func TestCreateGroup(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	creator := mustCreateUser(t, s, "creator")
	memberA := mustCreateUser(t, s, "member_a")
	memberB := mustCreateUser(t, s, "member_b")

	room, err := s.CreateGroup(ctx, creator, "trip-planning", nil, []uuid.UUID{memberA, memberB})
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if room.Type != models.RoomTypeGroup {
		t.Errorf("got type %q, want %q", room.Type, models.RoomTypeGroup)
	}
	if room.IsPublic {
		t.Error("a group room must never be public")
	}

	members, err := s.ListMembers(ctx, room.ID)
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	if len(members) != 3 {
		t.Fatalf("got %d members, want 3 (creator + 2)", len(members))
	}

	creatorMembership, err := s.GetMembership(ctx, room.ID, creator)
	if err != nil {
		t.Fatalf("GetMembership(creator): %v", err)
	}
	if creatorMembership.Role != models.RoleAdmin {
		t.Errorf("got creator role %q, want %q", creatorMembership.Role, models.RoleAdmin)
	}

	memberAMembership, err := s.GetMembership(ctx, room.ID, memberA)
	if err != nil {
		t.Fatalf("GetMembership(memberA): %v", err)
	}
	if memberAMembership.Role != models.RoleMember {
		t.Errorf("got memberA role %q, want %q", memberAMembership.Role, models.RoleMember)
	}
}

func TestChangeMemberRole(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	creator := mustCreateUser(t, s, "creator")
	member := mustCreateUser(t, s, "member")

	room, err := s.CreateChannel(ctx, creator, "team", nil, true)
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	if _, err := s.AddMember(ctx, room.ID, member, models.RoleMember); err != nil {
		t.Fatalf("AddMember: %v", err)
	}

	t.Run("promotes a member to admin", func(t *testing.T) {
		changed, err := s.ChangeMemberRole(ctx, room.ID, member, models.RoleAdmin)
		if err != nil {
			t.Fatalf("ChangeMemberRole (promote): %v", err)
		}
		if !changed {
			t.Error("expected changed=true for a genuine promotion")
		}
		m, err := s.GetMembership(ctx, room.ID, member)
		if err != nil {
			t.Fatalf("GetMembership: %v", err)
		}
		if m.Role != models.RoleAdmin {
			t.Errorf("got role %q, want %q", m.Role, models.RoleAdmin)
		}
	})

	t.Run("re-setting the same role is a no-op", func(t *testing.T) {
		changed, err := s.ChangeMemberRole(ctx, room.ID, member, models.RoleAdmin)
		if err != nil {
			t.Fatalf("ChangeMemberRole (no-op): %v", err)
		}
		if changed {
			t.Error("expected changed=false when the role didn't actually change")
		}
	})

	t.Run("demotes back to member (two admins present)", func(t *testing.T) {
		changed, err := s.ChangeMemberRole(ctx, room.ID, member, models.RoleMember)
		if err != nil {
			t.Fatalf("ChangeMemberRole (demote): %v", err)
		}
		if !changed {
			t.Error("expected changed=true for a genuine demotion")
		}
	})

	t.Run("demoting the last remaining admin is rejected", func(t *testing.T) {
		if _, err := s.ChangeMemberRole(ctx, room.ID, creator, models.RoleMember); !errors.Is(err, store.ErrLastAdmin) {
			t.Errorf("got error %v, want ErrLastAdmin", err)
		}
		// And the role must be unchanged after the rejection.
		m, err := s.GetMembership(ctx, room.ID, creator)
		if err != nil {
			t.Fatalf("GetMembership: %v", err)
		}
		if m.Role != models.RoleAdmin {
			t.Errorf("got role %q after a rejected demote, want it to stay %q", m.Role, models.RoleAdmin)
		}
	})

	t.Run("changing role of a non-member 404s", func(t *testing.T) {
		stranger := mustCreateUser(t, s, "stranger")
		if _, err := s.ChangeMemberRole(ctx, room.ID, stranger, models.RoleAdmin); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("got error %v, want ErrNotFound", err)
		}
	})
}

func TestPromoteOldestIfNoAdmins(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	creator := mustCreateUser(t, s, "creator")
	older := mustCreateUser(t, s, "older_member")
	younger := mustCreateUser(t, s, "younger_member")

	room, err := s.CreateChannel(ctx, creator, "team", nil, true)
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	if _, err := s.AddMember(ctx, room.ID, older, models.RoleMember); err != nil {
		t.Fatalf("AddMember(older): %v", err)
	}
	time.Sleep(2 * time.Millisecond) // ensure a strictly later joined_at than `older`
	if _, err := s.AddMember(ctx, room.ID, younger, models.RoleMember); err != nil {
		t.Fatalf("AddMember(younger): %v", err)
	}

	t.Run("no-ops while an admin remains", func(t *testing.T) {
		promoted, err := s.PromoteOldestIfNoAdmins(ctx, room.ID)
		if err != nil {
			t.Fatalf("PromoteOldestIfNoAdmins: %v", err)
		}
		if promoted != nil {
			t.Errorf("expected no promotion while the creator is still admin, got %+v", promoted)
		}
	})

	t.Run("promotes the oldest remaining member once the sole admin leaves", func(t *testing.T) {
		if err := s.RemoveMember(ctx, room.ID, creator); err != nil {
			t.Fatalf("RemoveMember(creator): %v", err)
		}

		promoted, err := s.PromoteOldestIfNoAdmins(ctx, room.ID)
		if err != nil {
			t.Fatalf("PromoteOldestIfNoAdmins: %v", err)
		}
		if promoted == nil {
			t.Fatal("expected a promotion once the room has zero admins")
		}
		if promoted.UserID != older {
			t.Errorf("got promoted user %s, want %s (the older remaining member)", promoted.UserID, older)
		}
		if promoted.Role != models.RoleAdmin {
			t.Errorf("got promoted role %q, want %q", promoted.Role, models.RoleAdmin)
		}

		// A second call must no-op now that the room has an admin again.
		second, err := s.PromoteOldestIfNoAdmins(ctx, room.ID)
		if err != nil {
			t.Fatalf("PromoteOldestIfNoAdmins (second call): %v", err)
		}
		if second != nil {
			t.Errorf("expected no further promotion, got %+v", second)
		}
	})

	t.Run("no-ops on a direct room even with zero admins (DMs have none by design)", func(t *testing.T) {
		alice := mustCreateUser(t, s, "dm_alice")
		bob := mustCreateUser(t, s, "dm_bob")
		dm, _, _, err := s.GetOrCreateDirectRoom(ctx, alice, bob)
		if err != nil {
			t.Fatalf("GetOrCreateDirectRoom: %v", err)
		}
		promoted, err := s.PromoteOldestIfNoAdmins(ctx, dm.ID)
		if err != nil {
			t.Fatalf("PromoteOldestIfNoAdmins(direct): %v", err)
		}
		if promoted != nil {
			t.Errorf("expected no promotion on a direct room, got %+v", promoted)
		}
	})
}

func TestListAllRooms(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	alice := mustCreateUser(t, s, "admin_view_alice")
	bob := mustCreateUser(t, s, "admin_view_bob")
	outsider := mustCreateUser(t, s, "admin_view_outsider")

	channel, err := s.CreateChannel(ctx, alice, "team-channel", nil, true)
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	if _, err := s.AddMember(ctx, channel.ID, bob, models.RoleMember); err != nil {
		t.Fatalf("AddMember: %v", err)
	}
	dm, _, _, err := s.GetOrCreateDirectRoom(ctx, alice, bob)
	if err != nil {
		t.Fatalf("GetOrCreateDirectRoom: %v", err)
	}

	// outsider belongs to neither room — ListAllRooms must still return both,
	// unlike ListRoomsForUser, which is scoped to the caller's own membership.
	rooms, err := s.ListAllRooms(ctx, 50, 0)
	if err != nil {
		t.Fatalf("ListAllRooms: %v", err)
	}

	byID := make(map[uuid.UUID]models.AdminRoomSummary, len(rooms))
	for _, r := range rooms {
		byID[r.ID] = r
	}
	if _, ok := byID[channel.ID]; !ok {
		t.Error("expected the channel to appear in ListAllRooms")
	}
	if _, ok := byID[dm.ID]; !ok {
		t.Error("expected the direct room to appear in ListAllRooms")
	}
	if got := byID[channel.ID].MemberCount; got != 2 {
		t.Errorf("got channel member_count %d, want 2 (alice + bob)", got)
	}
	if got := byID[channel.ID].Creator.ID; got != alice {
		t.Errorf("got creator %s, want %s", got, alice)
	}

	t.Run("outsider's own membership doesn't affect the listing", func(t *testing.T) {
		rooms, err := s.ListRoomsForUser(ctx, outsider)
		if err != nil {
			t.Fatalf("ListRoomsForUser: %v", err)
		}
		if len(rooms) != 0 {
			t.Fatalf("sanity check failed: outsider unexpectedly belongs to %d rooms", len(rooms))
		}
	})

	t.Run("limit is respected", func(t *testing.T) {
		limited, err := s.ListAllRooms(ctx, 1, 0)
		if err != nil {
			t.Fatalf("ListAllRooms: %v", err)
		}
		if len(limited) != 1 {
			t.Errorf("got %d rooms, want 1", len(limited))
		}
	})
}

func TestListMembers_And_ListRoomsForUser(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	creator := mustCreateUser(t, s, "creator")
	memberA := mustCreateUser(t, s, "member_a")
	outsider := mustCreateUser(t, s, "outsider")

	room, err := s.CreateChannel(ctx, creator, "team", nil, true)
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	if _, err := s.AddMember(ctx, room.ID, memberA, models.RoleMember); err != nil {
		t.Fatalf("AddMember: %v", err)
	}

	members, err := s.ListMembers(ctx, room.ID)
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("got %d members, want 2", len(members))
	}

	rooms, err := s.ListRoomsForUser(ctx, creator)
	if err != nil {
		t.Fatalf("ListRoomsForUser(creator): %v", err)
	}
	if len(rooms) != 1 || rooms[0].ID != room.ID {
		t.Errorf("got %v, want exactly [%s]", rooms, room.ID)
	}

	outsiderRooms, err := s.ListRoomsForUser(ctx, outsider)
	if err != nil {
		t.Fatalf("ListRoomsForUser(outsider): %v", err)
	}
	if len(outsiderRooms) != 0 {
		t.Errorf("got %d rooms for an outsider, want 0", len(outsiderRooms))
	}
}

func TestListRoomsForUser_UnreadCount(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	creator := mustCreateUser(t, s, "creator")
	member := mustCreateUser(t, s, "member")

	room, err := s.CreateChannel(ctx, creator, "team", nil, true)
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	if _, err := s.AddMember(ctx, room.ID, member, models.RoleMember); err != nil {
		t.Fatalf("AddMember: %v", err)
	}

	unreadFor := func(userID uuid.UUID) int {
		t.Helper()
		rooms, err := s.ListRoomsForUser(ctx, userID)
		if err != nil {
			t.Fatalf("ListRoomsForUser: %v", err)
		}
		if len(rooms) != 1 {
			t.Fatalf("got %d rooms, want 1", len(rooms))
		}
		return rooms[0].UnreadCount
	}

	// Fresh room, nothing sent → zero unread for everyone.
	if got := unreadFor(member); got != 0 {
		t.Errorf("fresh room unread = %d, want 0", got)
	}

	// The creator sends two messages. They don't count toward the creator's
	// own unread, but they do count for the other member (who's never opened
	// the room, so their cursor COALESCEs to joined_at).
	msg1, err := s.InsertMessage(ctx, room.ID, creator, "hello", models.MessageTypeText)
	if err != nil {
		t.Fatalf("InsertMessage 1: %v", err)
	}
	if _, err := s.InsertMessage(ctx, room.ID, creator, "world", models.MessageTypeText); err != nil {
		t.Fatalf("InsertMessage 2: %v", err)
	}

	if got := unreadFor(creator); got != 0 {
		t.Errorf("own messages counted as unread: got %d, want 0", got)
	}
	if got := unreadFor(member); got != 2 {
		t.Errorf("member unread = %d, want 2", got)
	}

	// A deleted message must not inflate the count.
	if _, err := s.SoftDeleteMessage(ctx, msg1.ID); err != nil {
		t.Fatalf("SoftDeleteMessage: %v", err)
	}
	if got := unreadFor(member); got != 1 {
		t.Errorf("member unread after delete = %d, want 1", got)
	}

	// Marking the room read advances the cursor → back to zero.
	if err := s.AdvanceLastRead(ctx, room.ID, member); err != nil {
		t.Fatalf("AdvanceLastRead: %v", err)
	}
	if got := unreadFor(member); got != 0 {
		t.Errorf("member unread after AdvanceLastRead = %d, want 0", got)
	}

	// A new message after the cursor counts again.
	if _, err := s.InsertMessage(ctx, room.ID, creator, "again", models.MessageTypeText); err != nil {
		t.Fatalf("InsertMessage 3: %v", err)
	}
	if got := unreadFor(member); got != 1 {
		t.Errorf("member unread after new message = %d, want 1", got)
	}
}

func TestAdvanceLastRead_NotAMember(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	creator := mustCreateUser(t, s, "creator")
	outsider := mustCreateUser(t, s, "outsider")

	room, err := s.CreateChannel(ctx, creator, "team", nil, true)
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}

	err = s.AdvanceLastRead(ctx, room.ID, outsider)
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("AdvanceLastRead for a non-member: got %v, want ErrNotFound", err)
	}
}

func TestListPublicChannels(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	creator := mustCreateUser(t, s, "creator")
	member := mustCreateUser(t, s, "member")
	browser := mustCreateUser(t, s, "browser")

	publicRoom, err := s.CreateChannel(ctx, creator, "general", nil, true)
	if err != nil {
		t.Fatalf("CreateChannel(public): %v", err)
	}
	if _, err := s.AddMember(ctx, publicRoom.ID, member, models.RoleMember); err != nil {
		t.Fatalf("AddMember: %v", err)
	}

	privateRoom, err := s.CreateChannel(ctx, creator, "secret", nil, false)
	if err != nil {
		t.Fatalf("CreateChannel(private): %v", err)
	}

	if _, _, _, err := s.GetOrCreateDirectRoom(ctx, creator, browser); err != nil {
		t.Fatalf("GetOrCreateDirectRoom: %v", err)
	}

	t.Run("lists public channels with member counts, excluding private and direct rooms", func(t *testing.T) {
		channels, err := s.ListPublicChannels(ctx, browser)
		if err != nil {
			t.Fatalf("ListPublicChannels: %v", err)
		}
		if len(channels) != 1 {
			t.Fatalf("got %d channels, want 1: %+v", len(channels), channels)
		}
		if channels[0].ID != publicRoom.ID {
			t.Errorf("got room %s, want %s", channels[0].ID, publicRoom.ID)
		}
		if channels[0].MemberCount != 2 {
			t.Errorf("got member_count %d, want 2 (creator + member)", channels[0].MemberCount)
		}
		for _, c := range channels {
			if c.ID == privateRoom.ID {
				t.Error("private channel must not appear in the public list")
			}
		}
	})

	t.Run("excludes channels the caller already belongs to", func(t *testing.T) {
		channels, err := s.ListPublicChannels(ctx, creator)
		if err != nil {
			t.Fatalf("ListPublicChannels: %v", err)
		}
		for _, c := range channels {
			if c.ID == publicRoom.ID {
				t.Error("a channel the caller already belongs to must not appear")
			}
		}
	})

	t.Run("a departed member sees the channel again", func(t *testing.T) {
		if err := s.RemoveMember(ctx, publicRoom.ID, member); err != nil {
			t.Fatalf("RemoveMember: %v", err)
		}
		channels, err := s.ListPublicChannels(ctx, member)
		if err != nil {
			t.Fatalf("ListPublicChannels: %v", err)
		}
		found := false
		for _, c := range channels {
			if c.ID == publicRoom.ID {
				found = true
				if c.MemberCount != 1 {
					t.Errorf("got member_count %d after leaving, want 1 (creator only)", c.MemberCount)
				}
			}
		}
		if !found {
			t.Error("expected the departed member to see the channel again in the public list")
		}
	})
}
