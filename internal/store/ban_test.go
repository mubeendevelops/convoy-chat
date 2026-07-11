package store_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/mubeendevelops/convoy-chat/internal/models"
	"github.com/mubeendevelops/convoy-chat/internal/store"
	"github.com/mubeendevelops/convoy-chat/internal/testutil"
)

func containsPublicChannel(channels []*models.PublicChannel, id uuid.UUID) bool {
	for _, c := range channels {
		if c.ID == id {
			return true
		}
	}
	return false
}

// TestBanMember covers the kick-bans-rejoin lifecycle at the store layer: a
// ban sets banned_at (blocking IsBanned), re-banning an already-departed row
// 404s, and an admin re-invite (AddMember) clears the ban and reactivates.
func TestBanMember(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	admin := mustCreateUser(t, s, "ban_admin")
	target := mustCreateUser(t, s, "ban_target")

	desc := "ban room"
	room, err := s.CreateChannel(ctx, admin, "ban-room", &desc, true)
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	if _, err := s.AddMember(ctx, room.ID, target, models.RoleMember); err != nil {
		t.Fatalf("AddMember: %v", err)
	}

	if banned, err := s.IsBanned(ctx, room.ID, target); err != nil || banned {
		t.Fatalf("IsBanned before kick: got (%v, %v), want (false, nil)", banned, err)
	}

	if err := s.BanMember(ctx, room.ID, target); err != nil {
		t.Fatalf("BanMember: %v", err)
	}
	if banned, err := s.IsBanned(ctx, room.ID, target); err != nil || !banned {
		t.Fatalf("IsBanned after kick: got (%v, %v), want (true, nil)", banned, err)
	}

	// Banning an already-departed member 404s (no active row to update).
	if err := s.BanMember(ctx, room.ID, target); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("re-ban: got %v, want ErrNotFound", err)
	}

	// Admin re-invite reactivates and lifts the ban.
	if _, err := s.AddMember(ctx, room.ID, target, models.RoleMember); err != nil {
		t.Fatalf("re-invite AddMember: %v", err)
	}
	if banned, err := s.IsBanned(ctx, room.ID, target); err != nil || banned {
		t.Fatalf("IsBanned after re-invite: got (%v, %v), want (false, nil)", banned, err)
	}
	if _, err := s.GetMembership(ctx, room.ID, target); err != nil {
		t.Errorf("GetMembership after re-invite: %v (want an active membership)", err)
	}
}

// TestListPublicChannels_ExcludesBanned confirms a channel a user is banned
// from disappears from their browse list, even though they aren't an active
// member (so the existing active-member exclusion alone wouldn't hide it).
func TestListPublicChannels_ExcludesBanned(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	admin := mustCreateUser(t, s, "browse_admin")
	target := mustCreateUser(t, s, "browse_target")

	desc := "browsable"
	room, err := s.CreateChannel(ctx, admin, "browse-room", &desc, true)
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}

	channels, err := s.ListPublicChannels(ctx, target)
	if err != nil {
		t.Fatalf("ListPublicChannels: %v", err)
	}
	if !containsPublicChannel(channels, room.ID) {
		t.Fatalf("want room %s in the browse list before any ban", room.ID)
	}

	if _, err := s.AddMember(ctx, room.ID, target, models.RoleMember); err != nil {
		t.Fatalf("AddMember: %v", err)
	}
	if err := s.BanMember(ctx, room.ID, target); err != nil {
		t.Fatalf("BanMember: %v", err)
	}

	channels, err = s.ListPublicChannels(ctx, target)
	if err != nil {
		t.Fatalf("ListPublicChannels after ban: %v", err)
	}
	if containsPublicChannel(channels, room.ID) {
		t.Errorf("banned channel must not appear in the browse list")
	}
}
