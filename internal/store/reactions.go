package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/mubeendevelops/convoy-chat/internal/models"
)

// ToggleReaction adds userID's emoji reaction to messageID if they haven't
// already reacted with it, or removes it if they have. added reports which
// happened.
//
// This is one statement (a DELETE feeding a conditional INSERT via a CTE),
// but a single statement alone isn't enough to be race-free: if the reaction
// doesn't exist yet, several concurrent toggles can all see "nothing to
// delete" and all fall through to INSERT the same (message_id, user_id,
// emoji) key — there's no existing row for them to serialize on the way
// there is when a row already exists to DELETE. ON CONFLICT DO NOTHING
// absorbs that race at the database level (exactly one insert wins, the
// rest no-op instead of erroring); the two EXISTS checks then let Go tell
// "I deleted it" apart from "someone else concurrently added it, so mine
// was suppressed" without relying on ambiguous "zero rows returned"
// semantics, which is exactly what the previous, simpler version got wrong
// (caught by TestToggleReaction_ConcurrentSameUserSameEmoji).
func (s *Store) ToggleReaction(ctx context.Context, messageID, userID uuid.UUID, emoji string) (added bool, err error) {
	const q = `
		WITH deleted AS (
			DELETE FROM message_reactions
			WHERE message_id = $1 AND user_id = $2 AND emoji = $3
			RETURNING 1
		),
		inserted AS (
			INSERT INTO message_reactions (id, message_id, user_id, emoji)
			SELECT $4, $1, $2, $3
			WHERE NOT EXISTS (SELECT 1 FROM deleted)
			ON CONFLICT (message_id, user_id, emoji) DO NOTHING
			RETURNING 1
		)
		SELECT EXISTS (SELECT 1 FROM deleted), EXISTS (SELECT 1 FROM inserted)`

	var didDelete, didInsert bool
	if err := s.DB.QueryRow(ctx, q, messageID, userID, emoji, uuid.New()).Scan(&didDelete, &didInsert); err != nil {
		return false, fmt.Errorf("toggling reaction: %w", err)
	}
	// !didDelete covers both "we inserted it" and "a concurrent toggle beat
	// us to inserting the same key" — either way the end state is "present".
	return !didDelete, nil
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
