package store_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

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
