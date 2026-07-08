package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// MarkMessageRead upserts a read receipt for (messageID, userID). alreadyRead
// is true if the user had already marked this message read — a harmless,
// idempotent no-op; callers use it to skip a redundant broadcast rather than
// treating it as an error.
func (s *Store) MarkMessageRead(ctx context.Context, messageID, userID uuid.UUID) (alreadyRead bool, err error) {
	const q = `
		INSERT INTO message_read_receipts (id, message_id, user_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (message_id, user_id) DO NOTHING`

	tag, err := s.DB.Exec(ctx, q, uuid.New(), messageID, userID)
	if err != nil {
		return false, fmt.Errorf("marking message read: %w", err)
	}
	return tag.RowsAffected() == 0, nil
}

// ListReadByForMessages batch-fetches the user IDs who've read each of
// messageIDs, in one round trip (used by history listing — avoids an N+1
// query per message). Messages with no reads are simply absent from the map.
func (s *Store) ListReadByForMessages(ctx context.Context, messageIDs []uuid.UUID) (map[uuid.UUID][]uuid.UUID, error) {
	result := make(map[uuid.UUID][]uuid.UUID)
	if len(messageIDs) == 0 {
		return result, nil
	}

	const q = `
		SELECT message_id, user_id
		FROM message_read_receipts
		WHERE message_id = ANY($1)
		ORDER BY message_id, read_at ASC`

	rows, err := s.DB.Query(ctx, q, messageIDs)
	if err != nil {
		return nil, fmt.Errorf("querying message read receipts: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var messageID, userID uuid.UUID
		if err := rows.Scan(&messageID, &userID); err != nil {
			return nil, fmt.Errorf("scanning message read receipt: %w", err)
		}
		result[messageID] = append(result[messageID], userID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating message read receipts: %w", err)
	}

	return result, nil
}
