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

const messageWithAuthorColumns = `
	m.id, m.room_id, m.content, m.message_type, m.deleted_at, m.created_at, m.updated_at,
	u.id, u.username, u.avatar_url`

const messageIdempotencyTTL = 5 * time.Minute

// scanMessageWithAuthor scans a row produced by messageWithAuthorColumns.
// Content is left nil when the message is soft-deleted, so callers never see
// the original text through this shape even though it's retained in the row.
func scanMessageWithAuthor(row pgx.Row) (*models.MessageWithAuthor, error) {
	var m models.MessageWithAuthor
	var content string
	err := row.Scan(
		&m.ID, &m.RoomID, &content, &m.MessageType, &m.DeletedAt, &m.CreatedAt, &m.UpdatedAt,
		&m.User.ID, &m.User.Username, &m.User.AvatarURL,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scanning message: %w", err)
	}
	if m.DeletedAt == nil {
		m.Content = &content
	}
	return &m, nil
}

// InsertMessage inserts a new message and returns it joined with the
// author's public summary, so handlers can respond with the same shape used
// for history reads.
func (s *Store) InsertMessage(ctx context.Context, roomID, userID uuid.UUID, content string, messageType models.MessageType) (*models.MessageWithAuthor, error) {
	const q = `
		WITH inserted AS (
			INSERT INTO messages (id, room_id, user_id, content, message_type)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING id, room_id, user_id, content, message_type, deleted_at, created_at, updated_at
		)
		SELECT ` + messageWithAuthorColumns + `
		FROM inserted m
		JOIN users u ON u.id = m.user_id`

	row := s.DB.QueryRow(ctx, q, uuid.New(), roomID, userID, content, messageType)
	return scanMessageWithAuthor(row)
}

// ListRoomMessages returns up to limit messages in roomID, newest first. If
// before is non-nil, only messages older than that timestamp are included —
// callers page backward through history by passing the created_at of the
// oldest message from the previous page. Unlike an OFFSET, this stays
// correct even as new messages are inserted concurrently.
func (s *Store) ListRoomMessages(ctx context.Context, roomID uuid.UUID, limit int, before *time.Time) ([]models.MessageWithAuthor, error) {
	const q = `
		SELECT ` + messageWithAuthorColumns + `
		FROM messages m
		JOIN users u ON u.id = m.user_id
		WHERE m.room_id = $1 AND ($2::timestamptz IS NULL OR m.created_at < $2)
		ORDER BY m.created_at DESC, m.id DESC
		LIMIT $3`

	rows, err := s.DB.Query(ctx, q, roomID, before, limit)
	if err != nil {
		return nil, fmt.Errorf("querying room messages: %w", err)
	}
	defer rows.Close()

	messages := make([]models.MessageWithAuthor, 0)
	for rows.Next() {
		message, err := scanMessageWithAuthor(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, *message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating room messages: %w", err)
	}

	return messages, nil
}

// GetMessageByID returns the raw message row (no author join, not
// deletion-filtered) for internal use such as the author/admin check before
// a delete.
func (s *Store) GetMessageByID(ctx context.Context, id uuid.UUID) (*models.Message, error) {
	const q = `
		SELECT id, room_id, user_id, content, message_type, metadata, deleted_at, created_at, updated_at
		FROM messages
		WHERE id = $1`

	var m models.Message
	err := s.DB.QueryRow(ctx, q, id).Scan(
		&m.ID, &m.RoomID, &m.UserID, &m.Content, &m.MessageType, &m.Metadata, &m.DeletedAt, &m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scanning message: %w", err)
	}
	return &m, nil
}

// SoftDeleteMessage sets deleted_at on a message that isn't already deleted.
// Returns ErrNotFound if the message doesn't exist or was already deleted —
// messages are never hard-deleted.
func (s *Store) SoftDeleteMessage(ctx context.Context, id uuid.UUID) error {
	const q = `UPDATE messages SET deleted_at = NOW(), updated_at = NOW() WHERE id = $1 AND deleted_at IS NULL`

	tag, err := s.DB.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("soft-deleting message: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func messageIdempotencyKey(roomID, userID uuid.UUID, key string) string {
	return fmt.Sprintf("idempotency:message:%s:%s:%s", roomID, userID, key)
}

// ReserveMessageIdempotencyKey atomically claims key for roomID+userID. ok
// is false if it was already claimed within the TTL window, meaning this is
// a duplicate send that the caller should reject.
func (s *Store) ReserveMessageIdempotencyKey(ctx context.Context, roomID, userID uuid.UUID, key string) (ok bool, err error) {
	ok, err = s.Redis.SetNX(ctx, messageIdempotencyKey(roomID, userID, key), "1", messageIdempotencyTTL).Result()
	if err != nil {
		return false, fmt.Errorf("reserving message idempotency key: %w", err)
	}
	return ok, nil
}

// ReleaseMessageIdempotencyKey frees a previously reserved key after the
// associated insert failed, so a legitimate client retry isn't blocked for
// the rest of the TTL window.
func (s *Store) ReleaseMessageIdempotencyKey(ctx context.Context, roomID, userID uuid.UUID, key string) error {
	if err := s.Redis.Del(ctx, messageIdempotencyKey(roomID, userID, key)).Err(); err != nil {
		return fmt.Errorf("releasing message idempotency key: %w", err)
	}
	return nil
}
