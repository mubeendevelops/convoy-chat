package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mubeendevelops/convoy-chat/internal/models"
)

// ToggleReaction adds userID's emoji reaction to messageID if they haven't
// already reacted with it, or removes it if they have. added reports which
// happened. The two branches run as one statement (a DELETE feeding a
// conditional INSERT via a CTE) so a double-tap from the same user can't
// race itself into an inconsistent state — no explicit transaction needed,
// a single statement is already atomic.
func (s *Store) ToggleReaction(ctx context.Context, messageID, userID uuid.UUID, emoji string) (added bool, err error) {
	const q = `
		WITH deleted AS (
			DELETE FROM message_reactions
			WHERE message_id = $1 AND user_id = $2 AND emoji = $3
			RETURNING 1
		)
		INSERT INTO message_reactions (id, message_id, user_id, emoji)
		SELECT $4, $1, $2, $3
		WHERE NOT EXISTS (SELECT 1 FROM deleted)
		RETURNING id`

	var insertedID uuid.UUID
	err = s.DB.QueryRow(ctx, q, messageID, userID, emoji, uuid.New()).Scan(&insertedID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// The DELETE branch removed a row, so the INSERT's WHERE NOT
			// EXISTS suppressed it — this was a removal, not an addition.
			return false, nil
		}
		return false, fmt.Errorf("toggling reaction: %w", err)
	}
	return true, nil
}

// ListReactionsForMessages batch-fetches reactions for messageIDs in one
// round trip, grouped by message then by emoji (used by history listing —
// avoids an N+1 query per message). Messages with no reactions are simply
// absent from the map.
func (s *Store) ListReactionsForMessages(ctx context.Context, messageIDs []uuid.UUID) (map[uuid.UUID][]models.MessageReactionSummary, error) {
	result := make(map[uuid.UUID][]models.MessageReactionSummary)
	if len(messageIDs) == 0 {
		return result, nil
	}

	const q = `
		SELECT message_id, user_id, emoji
		FROM message_reactions
		WHERE message_id = ANY($1)
		ORDER BY message_id, created_at ASC`

	rows, err := s.DB.Query(ctx, q, messageIDs)
	if err != nil {
		return nil, fmt.Errorf("querying message reactions: %w", err)
	}
	defer rows.Close()

	// positions tracks where each (message, emoji) pair's summary already
	// landed in result[messageID], so repeat emoji rows accumulate into it
	// instead of creating duplicate entries.
	type emojiKey struct {
		messageID uuid.UUID
		emoji     string
	}
	positions := make(map[emojiKey]int)

	for rows.Next() {
		var messageID, userID uuid.UUID
		var emoji string
		if err := rows.Scan(&messageID, &userID, &emoji); err != nil {
			return nil, fmt.Errorf("scanning message reaction: %w", err)
		}

		key := emojiKey{messageID, emoji}
		if pos, ok := positions[key]; ok {
			result[messageID][pos].Count++
			result[messageID][pos].UserIDs = append(result[messageID][pos].UserIDs, userID)
			continue
		}
		result[messageID] = append(result[messageID], models.MessageReactionSummary{
			Emoji:   emoji,
			Count:   1,
			UserIDs: []uuid.UUID{userID},
		})
		positions[key] = len(result[messageID]) - 1
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating message reactions: %w", err)
	}

	return result, nil
}
