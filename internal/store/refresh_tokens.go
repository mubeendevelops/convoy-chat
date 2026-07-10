package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mubeendevelops/convoy-chat/internal/models"
)

const refreshColumns = "id, user_id, token_hash, family_id, created_at, expires_at, revoked_at"

func scanRefreshToken(row pgx.Row) (*models.RefreshToken, error) {
	var rt models.RefreshToken
	err := row.Scan(&rt.ID, &rt.UserID, &rt.TokenHash, &rt.FamilyID, &rt.CreatedAt, &rt.ExpiresAt, &rt.RevokedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scanning refresh token: %w", err)
	}
	return &rt, nil
}

// CreateRefreshToken inserts a brand-new refresh token row, starting a new
// rotation family (used at signup/login — every later rotation reuses this
// familyID via RotateRefreshToken).
func (s *Store) CreateRefreshToken(ctx context.Context, userID, familyID uuid.UUID, tokenHash string, expiresAt time.Time) (*models.RefreshToken, error) {
	rt := &models.RefreshToken{
		ID:        uuid.New(),
		UserID:    userID,
		TokenHash: tokenHash,
		FamilyID:  familyID,
		ExpiresAt: expiresAt,
	}

	const q = `
		INSERT INTO refresh_tokens (id, user_id, token_hash, family_id, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING created_at`

	if err := s.DB.QueryRow(ctx, q, rt.ID, rt.UserID, rt.TokenHash, rt.FamilyID, rt.ExpiresAt).Scan(&rt.CreatedAt); err != nil {
		return nil, fmt.Errorf("inserting refresh token: %w", err)
	}
	return rt, nil
}

// RotateRefreshToken consumes the refresh token identified by oldHash and
// issues a new one (newHash) in the same rotation family, all inside one
// transaction:
//
//   - No row matches oldHash at all → ErrNotFound (a bogus/garbage token).
//   - The row matches but is already revoked → ErrRefreshTokenReused, and —
//     because a revoked token being presented again means a client is using
//     a token that was already rotated past, the standard signal of a stolen
//     refresh token trailing the legitimate client's own next rotation — the
//     entire family is revoked too, not just this one request rejected.
//   - The row matches, is unrevoked, but has expired → ErrTokenExpired.
//   - Otherwise: the old row is marked revoked and a new row is inserted in
//     the same family, which is returned.
func (s *Store) RotateRefreshToken(ctx context.Context, oldHash, newHash string, newExpiresAt time.Time) (*models.RefreshToken, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const selectStmt = `SELECT ` + refreshColumns + ` FROM refresh_tokens WHERE token_hash = $1 FOR UPDATE`
	old, err := scanRefreshToken(tx.QueryRow(ctx, selectStmt, oldHash))
	if err != nil {
		return nil, err
	}

	if old.RevokedAt != nil {
		const revokeFamilyStmt = `UPDATE refresh_tokens SET revoked_at = NOW() WHERE family_id = $1 AND revoked_at IS NULL`
		if _, err := tx.Exec(ctx, revokeFamilyStmt, old.FamilyID); err != nil {
			return nil, fmt.Errorf("revoking refresh token family after reuse: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("committing transaction: %w", err)
		}
		return nil, ErrRefreshTokenReused
	}

	if old.ExpiresAt.Before(time.Now()) {
		return nil, ErrTokenExpired
	}

	const revokeStmt = `UPDATE refresh_tokens SET revoked_at = NOW() WHERE id = $1`
	if _, err := tx.Exec(ctx, revokeStmt, old.ID); err != nil {
		return nil, fmt.Errorf("revoking rotated-out refresh token: %w", err)
	}

	next := &models.RefreshToken{
		ID:        uuid.New(),
		UserID:    old.UserID,
		TokenHash: newHash,
		FamilyID:  old.FamilyID,
		ExpiresAt: newExpiresAt,
	}
	const insertStmt = `
		INSERT INTO refresh_tokens (id, user_id, token_hash, family_id, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING created_at`
	if err := tx.QueryRow(ctx, insertStmt, next.ID, next.UserID, next.TokenHash, next.FamilyID, next.ExpiresAt).Scan(&next.CreatedAt); err != nil {
		return nil, fmt.Errorf("inserting rotated refresh token: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}
	return next, nil
}

// GetRefreshTokenByHash looks up a refresh token by its hash, revoked or
// not — used by logout to find which family to revoke.
func (s *Store) GetRefreshTokenByHash(ctx context.Context, tokenHash string) (*models.RefreshToken, error) {
	row := s.DB.QueryRow(ctx, `SELECT `+refreshColumns+` FROM refresh_tokens WHERE token_hash = $1`, tokenHash)
	return scanRefreshToken(row)
}

// RevokeRefreshTokenFamily revokes every still-active token sharing familyID
// — used by logout (kills the presented session's whole rotation chain) and
// internally by RotateRefreshToken's reuse-detection path.
func (s *Store) RevokeRefreshTokenFamily(ctx context.Context, familyID uuid.UUID) error {
	const q = `UPDATE refresh_tokens SET revoked_at = NOW() WHERE family_id = $1 AND revoked_at IS NULL`
	if _, err := s.DB.Exec(ctx, q, familyID); err != nil {
		return fmt.Errorf("revoking refresh token family: %w", err)
	}
	return nil
}
