package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mubeendevelops/convoy-chat/internal/models"
)

// is_system_admin is appended rather than interleaved, same reasoning as
// rooms.go's roomColumns comment: Postgres always physically appends an
// ALTER TABLE ... ADD COLUMN (migration 007) regardless of where it's
// declared in models.User.
const userColumns = "id, username, email, password_hash, avatar_url, bio, created_at, updated_at, is_system_admin"

func scanUser(row pgx.Row) (*models.User, error) {
	var u models.User
	err := row.Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.AvatarURL, &u.Bio, &u.CreatedAt, &u.UpdatedAt, &u.IsSystemAdmin)
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

// PromoteToSystemAdmin grants system-admin status to the user with the given
// email (trimmed, lowercased — matching every other email lookup in this
// package). Used only by the `-promote-admin` CLI bootstrap mode (see
// cmd/api/promote.go) — no REST endpoint exists for this, by design (see
// plan.md's admin-dashboard proposal): granting the *first* system admin
// can't go through an admin-gated endpoint (nobody is one yet), and who else
// becomes one afterward is treated as an infrequent, high-stakes ops
// decision rather than an in-app action.
func (s *Store) PromoteToSystemAdmin(ctx context.Context, email string) (*models.User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	const q = `
		UPDATE users SET is_system_admin = true
		WHERE email = $1
		RETURNING ` + userColumns

	row := s.DB.QueryRow(ctx, q, email)
	return scanUser(row)
}

// likeEscaper neutralizes LIKE/ILIKE metacharacters so a caller-supplied
// search term is matched literally. Usernames may contain `_` (see the
// username regex in CLAUDE.md), which is a LIKE wildcard, so without this a
// search for "a_b" would also match "axb". `\` is Postgres's default LIKE
// escape character, so escaping to `\_`/`\%`/`\\` needs no explicit ESCAPE
// clause.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// SearchUsers returns up to limit users whose username starts with query
// (case-insensitive prefix), excluding excludeUserID (the caller — you can't
// invite yourself). When excludeRoomID is non-nil, users who are already
// active members of that room are excluded too, so an invite picker only
// surfaces people it can actually add. Returns UserSummary (never email or
// password_hash). query is assumed already trimmed and non-empty.
func (s *Store) SearchUsers(ctx context.Context, query string, excludeUserID uuid.UUID, excludeRoomID *uuid.UUID, limit int) ([]models.UserSummary, error) {
	const q = `
		SELECT u.id, u.username, u.avatar_url
		FROM users u
		WHERE u.username ILIKE $1
		  AND u.id <> $2
		  AND ($3::uuid IS NULL OR NOT EXISTS (
			SELECT 1 FROM room_members m
			WHERE m.room_id = $3::uuid AND m.user_id = u.id AND m.left_at IS NULL))
		ORDER BY u.username ASC
		LIMIT $4`

	pattern := likeEscaper.Replace(query) + "%"

	rows, err := s.DB.Query(ctx, q, pattern, excludeUserID, excludeRoomID, limit)
	if err != nil {
		return nil, fmt.Errorf("searching users: %w", err)
	}
	defer rows.Close()

	users := make([]models.UserSummary, 0)
	for rows.Next() {
		var u models.UserSummary
		if err := rows.Scan(&u.ID, &u.Username, &u.AvatarURL); err != nil {
			return nil, fmt.Errorf("scanning user summary: %w", err)
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating user search: %w", err)
	}

	return users, nil
}
