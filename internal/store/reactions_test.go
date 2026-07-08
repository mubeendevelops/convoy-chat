package store_test

import (
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/mubeendevelops/convoy-chat/internal/models"
	"github.com/mubeendevelops/convoy-chat/internal/testutil"
)

func TestToggleReaction(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	author := mustCreateUser(t, s, "author")
	reactor := mustCreateUser(t, s, "reactor")
	room := mustCreateRoom(t, s, author)
	msg, err := s.InsertMessage(ctx, room, author, "react to me", models.MessageTypeText)
	if err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}

	added, err := s.ToggleReaction(ctx, msg.ID, reactor, "👍")
	if err != nil {
		t.Fatalf("ToggleReaction (add): %v", err)
	}
	if !added {
		t.Fatal("expected the first toggle to add the reaction")
	}

	removed, err := s.ToggleReaction(ctx, msg.ID, reactor, "👍")
	if err != nil {
		t.Fatalf("ToggleReaction (remove): %v", err)
	}
	if removed {
		t.Fatal("expected the second toggle (same user, same emoji) to remove the reaction")
	}

	readdedAgain, err := s.ToggleReaction(ctx, msg.ID, reactor, "👍")
	if err != nil {
		t.Fatalf("ToggleReaction (re-add): %v", err)
	}
	if !readdedAgain {
		t.Fatal("expected the third toggle to add it back")
	}
}

func TestListReactionsForMessages_GroupsByEmoji(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	author := mustCreateUser(t, s, "author")
	userA := mustCreateUser(t, s, "reactor_a")
	userB := mustCreateUser(t, s, "reactor_b")
	room := mustCreateRoom(t, s, author)
	msg, err := s.InsertMessage(ctx, room, author, "react to me", models.MessageTypeText)
	if err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}

	if _, err := s.ToggleReaction(ctx, msg.ID, userA, "👍"); err != nil {
		t.Fatalf("ToggleReaction: %v", err)
	}
	if _, err := s.ToggleReaction(ctx, msg.ID, userB, "👍"); err != nil {
		t.Fatalf("ToggleReaction: %v", err)
	}
	if _, err := s.ToggleReaction(ctx, msg.ID, userA, "🎉"); err != nil {
		t.Fatalf("ToggleReaction: %v", err)
	}

	grouped, err := s.ListReactionsForMessages(ctx, []uuid.UUID{msg.ID})
	if err != nil {
		t.Fatalf("ListReactionsForMessages: %v", err)
	}

	summaries := grouped[msg.ID]
	if len(summaries) != 2 {
		t.Fatalf("got %d emoji groups, want 2", len(summaries))
	}

	byEmoji := map[string]models.MessageReactionSummary{}
	for _, s := range summaries {
		byEmoji[s.Emoji] = s
	}

	thumbsUp, ok := byEmoji["👍"]
	if !ok {
		t.Fatal("missing 👍 group")
	}
	if thumbsUp.Count != 2 || len(thumbsUp.UserIDs) != 2 {
		t.Errorf("got 👍 count=%d user_ids=%v, want count=2 with 2 users", thumbsUp.Count, thumbsUp.UserIDs)
	}

	party, ok := byEmoji["🎉"]
	if !ok {
		t.Fatal("missing 🎉 group")
	}
	if party.Count != 1 || len(party.UserIDs) != 1 || party.UserIDs[0] != userA {
		t.Errorf("got 🎉 %+v, want count=1 user_ids=[%s]", party, userA)
	}
}

// TestToggleReaction_ConcurrentSameUserSameEmoji is a regression test for the
// atomic DELETE-or-INSERT CTE: many concurrent toggles from the same user
// with the same emoji must never error (no unique-constraint violation) and
// must leave the reaction in a consistent, race-free end state.
func TestToggleReaction_ConcurrentSameUserSameEmoji(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	author := mustCreateUser(t, s, "author")
	reactor := mustCreateUser(t, s, "reactor")
	room := mustCreateRoom(t, s, author)
	msg, err := s.InsertMessage(ctx, room, author, "react to me", models.MessageTypeText)
	if err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}

	const attempts = 8
	errs := make([]error, attempts)
	var wg sync.WaitGroup
	wg.Add(attempts)
	for i := range attempts {
		go func(i int) {
			defer wg.Done()
			_, errs[i] = s.ToggleReaction(ctx, msg.ID, reactor, "👍")
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("attempt %d: unexpected error: %v", i, err)
		}
	}

	// End state must be consistent: at most one row for (message, user,
	// emoji) regardless of how the 8 toggles interleaved.
	grouped, err := s.ListReactionsForMessages(ctx, []uuid.UUID{msg.ID})
	if err != nil {
		t.Fatalf("ListReactionsForMessages: %v", err)
	}
	for _, summary := range grouped[msg.ID] {
		count := 0
		for _, id := range summary.UserIDs {
			if id == reactor {
				count++
			}
		}
		if count > 1 {
			t.Errorf("reactor appears %d times in emoji %q's user_ids, want at most 1", count, summary.Emoji)
		}
	}
}
