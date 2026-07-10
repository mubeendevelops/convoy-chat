package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mubeendevelops/convoy-chat/internal/store"
	"github.com/mubeendevelops/convoy-chat/internal/testutil"
)

func TestRotateRefreshToken_HappyPath(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	userID := mustCreateUser(t, s, "rotator")
	familyID := uuid.New()

	_, err := s.CreateRefreshToken(ctx, userID, familyID, "hash-v1", time.Now().Add(30*24*time.Hour))
	if err != nil {
		t.Fatalf("CreateRefreshToken: %v", err)
	}

	next, err := s.RotateRefreshToken(ctx, "hash-v1", "hash-v2", time.Now().Add(30*24*time.Hour))
	if err != nil {
		t.Fatalf("RotateRefreshToken: %v", err)
	}
	if next.FamilyID != familyID {
		t.Errorf("rotated token family = %v, want %v (same family as the original)", next.FamilyID, familyID)
	}
	if next.UserID != userID {
		t.Errorf("rotated token user = %v, want %v", next.UserID, userID)
	}
	if next.RevokedAt != nil {
		t.Error("a freshly rotated token should not already be revoked")
	}

	// The old token is now revoked, so rotating it a second time is a reuse.
	if _, err := s.RotateRefreshToken(ctx, "hash-v1", "hash-v3", time.Now().Add(30*24*time.Hour)); !errors.Is(err, store.ErrRefreshTokenReused) {
		t.Fatalf("RotateRefreshToken (replay of old token) = %v, want ErrRefreshTokenReused", err)
	}
}

func TestRotateRefreshToken_NotFound(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()

	if _, err := s.RotateRefreshToken(ctx, "no-such-hash", "hash-v2", time.Now().Add(time.Hour)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("RotateRefreshToken (bogus hash) = %v, want ErrNotFound", err)
	}
}

func TestRotateRefreshToken_Expired(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	userID := mustCreateUser(t, s, "expiree")

	_, err := s.CreateRefreshToken(ctx, userID, uuid.New(), "hash-expired", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("CreateRefreshToken: %v", err)
	}

	if _, err := s.RotateRefreshToken(ctx, "hash-expired", "hash-v2", time.Now().Add(time.Hour)); !errors.Is(err, store.ErrTokenExpired) {
		t.Fatalf("RotateRefreshToken (expired) = %v, want ErrTokenExpired", err)
	}
}

// TestRotateRefreshToken_ReuseRevokesWholeFamily is the security property the
// whole design exists for: replaying a token that's already been rotated
// past doesn't just fail that one request, it kills every other token in the
// same family too — including ones issued *after* the replayed one, which a
// stolen-token attacker wouldn't know about.
func TestRotateRefreshToken_ReuseRevokesWholeFamily(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	userID := mustCreateUser(t, s, "victim")
	familyID := uuid.New()

	_, err := s.CreateRefreshToken(ctx, userID, familyID, "hash-gen1", time.Now().Add(30*24*time.Hour))
	if err != nil {
		t.Fatalf("CreateRefreshToken: %v", err)
	}
	// Legitimate rotation: gen1 -> gen2.
	if _, err := s.RotateRefreshToken(ctx, "hash-gen1", "hash-gen2", time.Now().Add(30*24*time.Hour)); err != nil {
		t.Fatalf("RotateRefreshToken (gen1->gen2): %v", err)
	}
	// Attacker replays the stolen, already-rotated-out gen1 token.
	if _, err := s.RotateRefreshToken(ctx, "hash-gen1", "hash-attacker", time.Now().Add(30*24*time.Hour)); !errors.Is(err, store.ErrRefreshTokenReused) {
		t.Fatalf("RotateRefreshToken (replay) = %v, want ErrRefreshTokenReused", err)
	}
	// The legitimate holder's gen2 token — issued before the replay was even
	// detected — must now be dead too, not just gen1.
	if _, err := s.RotateRefreshToken(ctx, "hash-gen2", "hash-gen3", time.Now().Add(30*24*time.Hour)); !errors.Is(err, store.ErrRefreshTokenReused) {
		t.Fatalf("RotateRefreshToken (gen2, after family revoked by replay) = %v, want ErrRefreshTokenReused", err)
	}
}

func TestRevokeRefreshTokenFamily(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()
	userID := mustCreateUser(t, s, "logout-user")
	familyID := uuid.New()

	_, err := s.CreateRefreshToken(ctx, userID, familyID, "hash-logout", time.Now().Add(30*24*time.Hour))
	if err != nil {
		t.Fatalf("CreateRefreshToken: %v", err)
	}

	if err := s.RevokeRefreshTokenFamily(ctx, familyID); err != nil {
		t.Fatalf("RevokeRefreshTokenFamily: %v", err)
	}

	if _, err := s.RotateRefreshToken(ctx, "hash-logout", "hash-post-logout", time.Now().Add(time.Hour)); !errors.Is(err, store.ErrRefreshTokenReused) {
		t.Fatalf("RotateRefreshToken (after logout) = %v, want ErrRefreshTokenReused", err)
	}
}

func TestGetRefreshTokenByHash_NotFound(t *testing.T) {
	s := testutil.NewStore(t)
	ctx := t.Context()

	if _, err := s.GetRefreshTokenByHash(ctx, "does-not-exist"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetRefreshTokenByHash (bogus hash) = %v, want ErrNotFound", err)
	}
}
