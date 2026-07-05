package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mubeendevelops/convoy-chat/internal/models"
)

const userColumns = "id, username, email, password_hash, avatar_url, bio, created_at, updated_at"

func scanUser(row pgx.Row) (*models.User, error) {
	var u models.User
	err := row.Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.AvatarURL, &u.Bio, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scanning user: %w", err)
	}
	return &u, nil
}

// CreateUser inserts a new user with an app-generated UUID. passwordHash
// must already be a bcrypt hash — the store never hashes or compares
// passwords itself.
func (s *Store) CreateUser(ctx context.Context, username, email, passwordHash string) (*models.User, error) {
	user := &models.User{
		ID:           uuid.New(),
		Username:     username,
		Email:        email,
		PasswordHash: passwordHash,
	}

	const q = `
		INSERT INTO users (id, username, email, password_hash)
		VALUES ($1, $2, $3, $4)
		RETURNING created_at, updated_at`

	err := s.DB.QueryRow(ctx, q, user.ID, user.Username, user.Email, user.PasswordHash).
		Scan(&user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation {
			switch pgErr.ConstraintName {
			case "users_username_key":
				return nil, ErrDuplicateUsername
			case "users_email_key":
				return nil, ErrDuplicateEmail
			}
		}
		return nil, fmt.Errorf("inserting user: %w", err)
	}

	return user, nil
}

func (s *Store) GetUserByID(ctx context.Context, id uuid.UUID) (*models.User, error) {
	row := s.DB.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id)
	return scanUser(row)
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (*models.User, error) {
	row := s.DB.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE email = $1`, email)
	return scanUser(row)
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (*models.User, error) {
	row := s.DB.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE username = $1`, username)
	return scanUser(row)
}
