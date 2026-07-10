package store_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/mubeendevelops/convoy-chat/internal/models"
	"github.com/mubeendevelops/convoy-chat/internal/store"
	"github.com/mubeendevelops/convoy-chat/internal/testutil"
)

func TestCreateUser_AndLookups(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()

	user, err := s.CreateUser(ctx, "alice", "alice@example.com", "bcrypt-hash-placeholder")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if user.ID == uuid.Nil {
		t.Fatal("expected a generated ID")
	}

	t.Run("GetUserByID", func(t *testing.T) {
		got, err := s.GetUserByID(ctx, user.ID)
		if err != nil {
			t.Fatalf("GetUserByID: %v", err)
		}
		if got.Username != "alice" {
			t.Errorf("got username %q, want %q", got.Username, "alice")
		}
	})

	t.Run("GetUserByEmail", func(t *testing.T) {
		got, err := s.GetUserByEmail(ctx, "alice@example.com")
		if err != nil {
			t.Fatalf("GetUserByEmail: %v", err)
		}
		if got.ID != user.ID {
			t.Errorf("got id %s, want %s", got.ID, user.ID)
		}
	})

	t.Run("GetUserByUsername", func(t *testing.T) {
		got, err := s.GetUserByUsername(ctx, "alice")
		if err != nil {
			t.Fatalf("GetUserByUsername: %v", err)
		}
		if got.ID != user.ID {
			t.Errorf("got id %s, want %s", got.ID, user.ID)
		}
	})

	t.Run("GetUserByID not found", func(t *testing.T) {
		_, err := s.GetUserByID(ctx, uuid.New())
		if !errors.Is(err, store.ErrNotFound) {
			t.Errorf("got error %v, want ErrNotFound", err)
		}
	})
}

func TestCreateUser_DuplicateUsername(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()

	if _, err := s.CreateUser(ctx, "bob", "bob@example.com", "hash"); err != nil {
		t.Fatalf("first CreateUser: %v", err)
	}

	_, err := s.CreateUser(ctx, "bob", "different@example.com", "hash")
	if !errors.Is(err, store.ErrDuplicateUsername) {
		t.Errorf("got error %v, want ErrDuplicateUsername", err)
	}
}

func TestCreateUser_DuplicateEmail(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()

	if _, err := s.CreateUser(ctx, "carol", "carol@example.com", "hash"); err != nil {
		t.Fatalf("first CreateUser: %v", err)
	}

	_, err := s.CreateUser(ctx, "carol2", "carol@example.com", "hash")
	if !errors.Is(err, store.ErrDuplicateEmail) {
		t.Errorf("got error %v, want ErrDuplicateEmail", err)
	}
}

func TestPromoteToSystemAdmin(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()

	// store.CreateUser doesn't normalize email casing itself — that happens
	// at the handler layer (see CLAUDE.md) — so the fixture stores it
	// already-lowercased, same as every other test in this file, and
	// PromoteToSystemAdmin's own normalization is exercised via the mixed-
	// case + whitespace input below instead.
	user, err := s.CreateUser(ctx, "dave", "dave@example.com", "hash")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if user.IsSystemAdmin {
		t.Fatal("a freshly-created user must not start as a system admin")
	}

	t.Run("promotes by email, case-insensitive", func(t *testing.T) {
		promoted, err := s.PromoteToSystemAdmin(ctx, "  DAVE@example.com  ")
		if err != nil {
			t.Fatalf("PromoteToSystemAdmin: %v", err)
		}
		if promoted.ID != user.ID {
			t.Errorf("got user %s, want %s", promoted.ID, user.ID)
		}
		if !promoted.IsSystemAdmin {
			t.Error("expected IsSystemAdmin=true on the returned user")
		}

		got, err := s.GetUserByID(ctx, user.ID)
		if err != nil {
			t.Fatalf("GetUserByID: %v", err)
		}
		if !got.IsSystemAdmin {
			t.Error("expected the promotion to persist")
		}
	})

	t.Run("unknown email is ErrNotFound", func(t *testing.T) {
		if _, err := s.PromoteToSystemAdmin(ctx, "nobody@example.com"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("got error %v, want ErrNotFound", err)
		}
	})
}

func usernamesOf(users []models.UserSummary) []string {
	names := make([]string, len(users))
	for i, u := range users {
		names[i] = u.Username
	}
	return names
}

func TestSearchUsers(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()

	mk := func(name string) uuid.UUID {
		u, err := s.CreateUser(ctx, name, name+"@example.com", "hash")
		if err != nil {
			t.Fatalf("CreateUser(%q): %v", name, err)
		}
		return u.ID
	}
	aliceID := mk("alice")
	mk("alicia")
	mk("alan")
	mk("bob")

	t.Run("prefix match, case-insensitive, sorted, excludes self", func(t *testing.T) {
		got, err := s.SearchUsers(ctx, "AL", aliceID, nil, 20)
		if err != nil {
			t.Fatalf("SearchUsers: %v", err)
		}
		// alice is the caller (excluded); alan + alicia match "al", sorted asc.
		want := []string{"alan", "alicia"}
		if names := usernamesOf(got); !reflect.DeepEqual(names, want) {
			t.Errorf("got %v, want %v", names, want)
		}
	})

	t.Run("no match returns empty, non-nil", func(t *testing.T) {
		got, err := s.SearchUsers(ctx, "zzz", aliceID, nil, 20)
		if err != nil {
			t.Fatalf("SearchUsers: %v", err)
		}
		if got == nil {
			t.Fatal("expected non-nil empty slice")
		}
		if len(got) != 0 {
			t.Errorf("got %v, want empty", usernamesOf(got))
		}
	})

	t.Run("limit is respected", func(t *testing.T) {
		got, err := s.SearchUsers(ctx, "al", aliceID, nil, 1)
		if err != nil {
			t.Fatalf("SearchUsers: %v", err)
		}
		if len(got) != 1 {
			t.Errorf("got %d results, want 1", len(got))
		}
	})

	t.Run("excludes active members of the given room", func(t *testing.T) {
		room, err := s.CreateChannel(ctx, aliceID, "general", nil, true)
		if err != nil {
			t.Fatalf("CreateChannel: %v", err)
		}
		// Add alan to the room; a search scoped to that room must omit him.
		alan, err := s.GetUserByUsername(ctx, "alan")
		if err != nil {
			t.Fatalf("GetUserByUsername: %v", err)
		}
		if _, err := s.AddMember(ctx, room.ID, alan.ID, models.RoleMember); err != nil {
			t.Fatalf("AddMember: %v", err)
		}

		got, err := s.SearchUsers(ctx, "al", aliceID, &room.ID, 20)
		if err != nil {
			t.Fatalf("SearchUsers: %v", err)
		}
		// alice: caller (excluded) + member; alan: now a member (excluded);
		// alicia: not a member → the only result.
		want := []string{"alicia"}
		if names := usernamesOf(got); !reflect.DeepEqual(names, want) {
			t.Errorf("got %v, want %v", names, want)
		}
	})

	t.Run("underscore is matched literally, not as a wildcard", func(t *testing.T) {
		mk("a_b")
		mk("axb")
		got, err := s.SearchUsers(ctx, "a_b", aliceID, nil, 20)
		if err != nil {
			t.Fatalf("SearchUsers: %v", err)
		}
		// Without LIKE escaping "a_b" would also match "axb".
		want := []string{"a_b"}
		if names := usernamesOf(got); !reflect.DeepEqual(names, want) {
			t.Errorf("got %v, want %v", names, want)
		}
	})
}
