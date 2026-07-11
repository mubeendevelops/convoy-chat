package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mubeendevelops/convoy-chat/internal/models"
	"github.com/mubeendevelops/convoy-chat/internal/store"
	"github.com/mubeendevelops/convoy-chat/internal/testutil"
)

// mustCreateRoom creates a channel with creator as its (only, admin) member.
func mustCreateRoom(t *testing.T, s *store.Store, creator uuid.UUID) uuid.UUID {
	t.Helper()
	room, err := s.CreateChannel(t.Context(), creator, "test-room", nil, true)
	if err != nil {
		t.Fatalf("creating fixture room: %v", err)
	}
	return room.ID
}

func TestInsertMessage(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	author := mustCreateUser(t, s, "author")
	room := mustCreateRoom(t, s, author)

	msg, err := s.InsertMessage(ctx, room, author, "hello world", models.MessageTypeText)
	if err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}
	if msg.Content == nil || *msg.Content != "hello world" {
		t.Errorf("got content %v, want %q", msg.Content, "hello world")
	}
	if msg.User.ID != author {
		t.Errorf("got author %s, want %s", msg.User.ID, author)
	}
	if msg.ReadBy == nil || len(msg.ReadBy) != 0 {
		t.Errorf("got read_by %v, want an empty (non-nil) slice", msg.ReadBy)
	}
	if msg.Reactions == nil || len(msg.Reactions) != 0 {
		t.Errorf("got reactions %v, want an empty (non-nil) slice", msg.Reactions)
	}
}

func TestListRoomMessages_KeysetPagination(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	author := mustCreateUser(t, s, "author")
	room := mustCreateRoom(t, s, author)

	// Insert 5 messages with a small gap between each so created_at is
	// strictly increasing (avoids a same-timestamp ordering ambiguity that
	// would make this test's assertions flaky rather than the pagination
	// logic being wrong).
	var inserted []*models.MessageWithAuthor
	for i := range 5 {
		msg, err := s.InsertMessage(ctx, room, author, contentFor(i), models.MessageTypeText)
		if err != nil {
			t.Fatalf("InsertMessage %d: %v", i, err)
		}
		inserted = append(inserted, msg)
		time.Sleep(2 * time.Millisecond)
	}

	// Page 1: newest 3 (msg-4, msg-3, msg-2).
	page1, err := s.ListRoomMessages(ctx, room, 3, nil)
	if err != nil {
		t.Fatalf("ListRoomMessages page 1: %v", err)
	}
	assertMessageOrder(t, page1, inserted[4], inserted[3], inserted[2])

	// Simulate a new message arriving in the room mid-scroll, between the
	// two page fetches — this is exactly the scenario keyset pagination is
	// chosen over OFFSET for (see CLAUDE.md).
	if _, err := s.InsertMessage(ctx, room, author, "arrived-mid-scroll", models.MessageTypeText); err != nil {
		t.Fatalf("inserting mid-scroll message: %v", err)
	}

	// Page 2, anchored on the oldest message from page 1: must be exactly
	// the next-older batch (msg-1, msg-0), with no skip and no repeat, and
	// must NOT include the mid-scroll message (it's newer than the anchor).
	oldestOfPage1 := page1[len(page1)-1].CreatedAt
	page2, err := s.ListRoomMessages(ctx, room, 3, &oldestOfPage1)
	if err != nil {
		t.Fatalf("ListRoomMessages page 2: %v", err)
	}
	assertMessageOrder(t, page2, inserted[1], inserted[0])
}

func contentFor(i int) string {
	return "msg-" + string(rune('0'+i))
}

func assertMessageOrder(t *testing.T, got []models.MessageWithAuthor, want ...*models.MessageWithAuthor) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d messages, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].ID != want[i].ID {
			t.Errorf("position %d: got message %s (%v), want %s (%v)", i, got[i].ID, got[i].Content, want[i].ID, want[i].Content)
		}
	}
}

func TestSoftDeleteMessage(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	author := mustCreateUser(t, s, "author")
	room := mustCreateRoom(t, s, author)

	msg, err := s.InsertMessage(ctx, room, author, "delete me", models.MessageTypeText)
	if err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}

	if _, err := s.SoftDeleteMessage(ctx, msg.ID); err != nil {
		t.Fatalf("SoftDeleteMessage: %v", err)
	}

	t.Run("history masks content but keeps the row", func(t *testing.T) {
		page, err := s.ListRoomMessages(ctx, room, 10, nil)
		if err != nil {
			t.Fatalf("ListRoomMessages: %v", err)
		}
		if len(page) != 1 {
			t.Fatalf("got %d messages, want 1 (soft-deleted, not omitted)", len(page))
		}
		if page[0].Content != nil {
			t.Errorf("got content %v, want nil (masked)", page[0].Content)
		}
		if page[0].DeletedAt == nil {
			t.Error("expected deleted_at to be set")
		}
	})

	t.Run("GetMessageByID still returns the unmasked row for internal checks", func(t *testing.T) {
		raw, err := s.GetMessageByID(ctx, msg.ID)
		if err != nil {
			t.Fatalf("GetMessageByID: %v", err)
		}
		if raw.Content != "delete me" {
			t.Errorf("got content %q, want the original text (unmasked)", raw.Content)
		}
	})

	t.Run("deleting an already-deleted message 404s", func(t *testing.T) {
		if _, err := s.SoftDeleteMessage(ctx, msg.ID); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("got error %v, want ErrNotFound", err)
		}
	})
}

func TestEditMessage(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	author := mustCreateUser(t, s, "author")
	room := mustCreateRoom(t, s, author)

	msg, err := s.InsertMessage(ctx, room, author, "original text", models.MessageTypeText)
	if err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}
	if msg.EditedAt != nil {
		t.Errorf("a freshly-inserted message should have a nil edited_at, got %v", msg.EditedAt)
	}

	editedAt, err := s.EditMessage(ctx, msg.ID, "edited text")
	if err != nil {
		t.Fatalf("EditMessage: %v", err)
	}
	if editedAt.IsZero() {
		t.Error("expected a non-zero edited_at")
	}

	t.Run("history reflects the new content and edited_at", func(t *testing.T) {
		page, err := s.ListRoomMessages(ctx, room, 10, nil)
		if err != nil {
			t.Fatalf("ListRoomMessages: %v", err)
		}
		if len(page) != 1 {
			t.Fatalf("got %d messages, want 1", len(page))
		}
		if page[0].Content == nil || *page[0].Content != "edited text" {
			t.Errorf("got content %v, want %q", page[0].Content, "edited text")
		}
		if page[0].EditedAt == nil || !page[0].EditedAt.Equal(editedAt) {
			t.Errorf("got edited_at %v, want %v", page[0].EditedAt, editedAt)
		}
	})

	t.Run("editing an already-deleted message 404s", func(t *testing.T) {
		if _, err := s.SoftDeleteMessage(ctx, msg.ID); err != nil {
			t.Fatalf("SoftDeleteMessage: %v", err)
		}
		if _, err := s.EditMessage(ctx, msg.ID, "should not apply"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("got error %v, want ErrNotFound", err)
		}
	})

	t.Run("editing a nonexistent message 404s", func(t *testing.T) {
		if _, err := s.EditMessage(ctx, uuid.New(), "irrelevant"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("got error %v, want ErrNotFound", err)
		}
	})
}

func TestMessageIdempotencyKey(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	author := mustCreateUser(t, s, "author")
	room := mustCreateRoom(t, s, author)

	reserved, err := s.ReserveMessageIdempotencyKey(ctx, room, author, "key-1")
	if err != nil {
		t.Fatalf("ReserveMessageIdempotencyKey (first): %v", err)
	}
	if !reserved {
		t.Fatal("expected the first reservation to succeed")
	}

	reservedAgain, err := s.ReserveMessageIdempotencyKey(ctx, room, author, "key-1")
	if err != nil {
		t.Fatalf("ReserveMessageIdempotencyKey (duplicate): %v", err)
	}
	if reservedAgain {
		t.Error("expected the duplicate reservation to fail (already claimed)")
	}

	if err := s.ReleaseMessageIdempotencyKey(ctx, room, author, "key-1"); err != nil {
		t.Fatalf("ReleaseMessageIdempotencyKey: %v", err)
	}

	reservedAfterRelease, err := s.ReserveMessageIdempotencyKey(ctx, room, author, "key-1")
	if err != nil {
		t.Fatalf("ReserveMessageIdempotencyKey (after release): %v", err)
	}
	if !reservedAfterRelease {
		t.Error("expected the key to be reservable again after release")
	}
}
