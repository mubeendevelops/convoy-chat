package store_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/mubeendevelops/convoy-chat/internal/models"
	"github.com/mubeendevelops/convoy-chat/internal/testutil"
)

func TestMarkMessageRead(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	author := mustCreateUser(t, s, "author")
	reader := mustCreateUser(t, s, "reader")
	room := mustCreateRoom(t, s, author)
	msg, err := s.InsertMessage(ctx, room, author, "read me", models.MessageTypeText)
	if err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}

	alreadyRead, err := s.MarkMessageRead(ctx, msg.ID, reader)
	if err != nil {
		t.Fatalf("MarkMessageRead (first): %v", err)
	}
	if alreadyRead {
		t.Error("expected the first mark-read to report alreadyRead=false")
	}

	alreadyReadAgain, err := s.MarkMessageRead(ctx, msg.ID, reader)
	if err != nil {
		t.Fatalf("MarkMessageRead (second): %v", err)
	}
	if !alreadyReadAgain {
		t.Error("expected the second mark-read (same message, same user) to report alreadyRead=true")
	}
}

func TestListReadByForMessages(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	author := mustCreateUser(t, s, "author")
	readerA := mustCreateUser(t, s, "reader_a")
	readerB := mustCreateUser(t, s, "reader_b")
	room := mustCreateRoom(t, s, author)

	msgRead, err := s.InsertMessage(ctx, room, author, "read by two", models.MessageTypeText)
	if err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}
	msgUnread, err := s.InsertMessage(ctx, room, author, "unread", models.MessageTypeText)
	if err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}

	if _, err := s.MarkMessageRead(ctx, msgRead.ID, readerA); err != nil {
		t.Fatalf("MarkMessageRead: %v", err)
	}
	if _, err := s.MarkMessageRead(ctx, msgRead.ID, readerB); err != nil {
		t.Fatalf("MarkMessageRead: %v", err)
	}

	grouped, err := s.ListReadByForMessages(ctx, []uuid.UUID{msgRead.ID, msgUnread.ID})
	if err != nil {
		t.Fatalf("ListReadByForMessages: %v", err)
	}

	readers := grouped[msgRead.ID]
	if len(readers) != 2 {
		t.Fatalf("got %d readers for msgRead, want 2", len(readers))
	}
	seen := map[uuid.UUID]bool{readers[0]: true, readers[1]: true}
	if !seen[readerA] || !seen[readerB] {
		t.Errorf("got readers %v, want both %s and %s", readers, readerA, readerB)
	}

	if _, ok := grouped[msgUnread.ID]; ok {
		t.Errorf("expected msgUnread to be absent from the map (no reads), got %v", grouped[msgUnread.ID])
	}
}

// TestListRoomMessages_EmbedsReadByAndReactions is an end-to-end check that
// history responses (not just the narrow batch-fetch methods in isolation)
// actually carry read_by and reactions once Phase 7 state exists for a
// message — this is the shape REST/WS clients actually consume.
func TestListRoomMessages_EmbedsReadByAndReactions(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	author := mustCreateUser(t, s, "author")
	other := mustCreateUser(t, s, "other")
	room := mustCreateRoom(t, s, author)

	msg, err := s.InsertMessage(ctx, room, author, "hello", models.MessageTypeText)
	if err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}
	if _, err := s.MarkMessageRead(ctx, msg.ID, other); err != nil {
		t.Fatalf("MarkMessageRead: %v", err)
	}
	if _, err := s.ToggleReaction(ctx, msg.ID, other, "👍"); err != nil {
		t.Fatalf("ToggleReaction: %v", err)
	}

	page, err := s.ListRoomMessages(ctx, room, 10, nil)
	if err != nil {
		t.Fatalf("ListRoomMessages: %v", err)
	}
	if len(page) != 1 {
		t.Fatalf("got %d messages, want 1", len(page))
	}

	got := page[0]
	if len(got.ReadBy) != 1 || got.ReadBy[0] != other {
		t.Errorf("got read_by %v, want [%s]", got.ReadBy, other)
	}
	if len(got.Reactions) != 1 || got.Reactions[0].Emoji != "👍" || got.Reactions[0].Count != 1 {
		t.Errorf("got reactions %+v, want one 👍 with count 1", got.Reactions)
	}
}
