package store_test

import (
	"errors"
	"sync"
	"testing"

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
	room, err := s.CreateChannel(ctx, creator, "general", &desc)
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

	room1, created1, err := s.GetOrCreateDirectRoom(ctx, alice, bob)
	if err != nil {
		t.Fatalf("GetOrCreateDirectRoom (first call): %v", err)
	}
	if !created1 {
		t.Error("expected created=true on the first call")
	}
	if room1.Type != models.RoomTypeDirect {
		t.Errorf("got type %q, want %q", room1.Type, models.RoomTypeDirect)
	}

	t.Run("second call from the same caller dedupes", func(t *testing.T) {
		room2, created2, err := s.GetOrCreateDirectRoom(ctx, alice, bob)
		if err != nil {
			t.Fatalf("GetOrCreateDirectRoom: %v", err)
		}
		if created2 {
			t.Error("expected created=false on the second call")
		}
		if room2.ID != room1.ID {
			t.Errorf("got a different room id %s, want %s", room2.ID, room1.ID)
		}
	})

	t.Run("reversed argument order dedupes to the same room", func(t *testing.T) {
		room3, created3, err := s.GetOrCreateDirectRoom(ctx, bob, alice)
		if err != nil {
			t.Fatalf("GetOrCreateDirectRoom: %v", err)
		}
		if created3 {
			t.Error("expected created=false when the peer calls back")
		}
		if room3.ID != room1.ID {
			t.Errorf("got a different room id %s, want %s", room3.ID, room1.ID)
		}
	})
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
			room, created, err := s.GetOrCreateDirectRoom(ctx, dave, erin)
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

	room, err := s.CreateChannel(ctx, creator, "team", nil)
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

func TestListMembers_And_ListRoomsForUser(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	creator := mustCreateUser(t, s, "creator")
	memberA := mustCreateUser(t, s, "member_a")
	outsider := mustCreateUser(t, s, "outsider")

	room, err := s.CreateChannel(ctx, creator, "team", nil)
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
