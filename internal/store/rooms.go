package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mubeendevelops/convoy-chat/internal/models"
)

// is_public is appended rather than interleaved because Postgres always
// physically appends an ALTER TABLE ... ADD COLUMN to the end of the row
// (migration 004), regardless of where it's declared in models.Room.
const roomColumns = "id, name, type, creator_id, description, avatar_url, is_archived, created_at, updated_at, is_public"

// querier is satisfied by both *pgxpool.Pool and pgx.Tx, so helpers below
// can run either standalone or inside a transaction.
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func scanRoom(row pgx.Row) (*models.Room, error) {
	var rm models.Room
	err := row.Scan(&rm.ID, &rm.Name, &rm.Type, &rm.CreatorID, &rm.Description, &rm.AvatarURL, &rm.IsArchived, &rm.CreatedAt, &rm.UpdatedAt, &rm.IsPublic)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scanning room: %w", err)
	}
	return &rm, nil
}

// insertMember adds a brand-new room_members row. Only safe to call when no
// row for (room_id, user_id) can already exist — e.g. during room creation,
// where room_id is freshly generated. For re-inviting a user who may already
// have a row (active or departed), use AddMember instead.
func insertMember(ctx context.Context, q querier, roomID, userID uuid.UUID, role models.MemberRole) error {
	const stmt = `INSERT INTO room_members (id, room_id, user_id, role) VALUES ($1, $2, $3, $4)`
	_, err := q.Exec(ctx, stmt, uuid.New(), roomID, userID, role)
	if err != nil {
		return fmt.Errorf("inserting room member: %w", err)
	}
	return nil
}

// CreateChannel creates a named channel room and adds creatorID as its admin
// member, atomically. isPublic controls whether the channel is browsable and
// self-joinable via ListPublicChannels/JoinChannel; it has no effect beyond
// that (membership is still admin-invite-only for a private channel, exactly
// as before this field existed).
func (s *Store) CreateChannel(ctx context.Context, creatorID uuid.UUID, name string, description *string, isPublic bool) (*models.Room, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	room := &models.Room{
		ID:          uuid.New(),
		Name:        &name,
		Type:        models.RoomTypeChannel,
		CreatorID:   creatorID,
		Description: description,
	}

	const insertRoomStmt = `
		INSERT INTO rooms (id, name, type, creator_id, description, is_public)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING is_archived, is_public, created_at, updated_at`

	err = tx.QueryRow(ctx, insertRoomStmt, room.ID, room.Name, room.Type, room.CreatorID, room.Description, isPublic).
		Scan(&room.IsArchived, &room.IsPublic, &room.CreatedAt, &room.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("inserting room: %w", err)
	}

	if err := insertMember(ctx, tx, room.ID, creatorID, models.RoleAdmin); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	return room, nil
}

// GetOrCreateDirectRoom returns the existing direct room between userA and
// userB, or creates one if none exists yet. created is true only when a new
// room was inserted. A Postgres advisory lock keyed on the sorted user pair
// serializes concurrent attempts so two simultaneous requests can't create
// two separate direct rooms for the same pair.
func (s *Store) GetOrCreateDirectRoom(ctx context.Context, userA, userB uuid.UUID) (*models.Room, bool, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const lockStmt = `SELECT pg_advisory_xact_lock(hashtextextended(least($1::text, $2::text) || ':' || greatest($1::text, $2::text), 0))`
	if _, err := tx.Exec(ctx, lockStmt, userA, userB); err != nil {
		return nil, false, fmt.Errorf("acquiring direct-room lock: %w", err)
	}

	const findStmt = `
		SELECT ` + roomColumns + `
		FROM rooms r
		WHERE r.type = 'direct'
		  AND EXISTS (SELECT 1 FROM room_members WHERE room_id = r.id AND user_id = $1 AND left_at IS NULL)
		  AND EXISTS (SELECT 1 FROM room_members WHERE room_id = r.id AND user_id = $2 AND left_at IS NULL)
		  AND (SELECT COUNT(*) FROM room_members WHERE room_id = r.id AND left_at IS NULL) = 2`

	room, err := scanRoom(tx.QueryRow(ctx, findStmt, userA, userB))
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return nil, false, fmt.Errorf("committing transaction: %w", err)
		}
		return room, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, false, err
	}

	newRoom := &models.Room{
		ID:        uuid.New(),
		Type:      models.RoomTypeDirect,
		CreatorID: userA,
	}

	const insertRoomStmt = `
		INSERT INTO rooms (id, type, creator_id)
		VALUES ($1, $2, $3)
		RETURNING is_archived, is_public, created_at, updated_at`

	err = tx.QueryRow(ctx, insertRoomStmt, newRoom.ID, newRoom.Type, newRoom.CreatorID).
		Scan(&newRoom.IsArchived, &newRoom.IsPublic, &newRoom.CreatedAt, &newRoom.UpdatedAt)
	if err != nil {
		return nil, false, fmt.Errorf("inserting direct room: %w", err)
	}

	if err := insertMember(ctx, tx, newRoom.ID, userA, models.RoleMember); err != nil {
		return nil, false, err
	}
	if err := insertMember(ctx, tx, newRoom.ID, userB, models.RoleMember); err != nil {
		return nil, false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("committing transaction: %w", err)
	}

	return newRoom, true, nil
}

func (s *Store) GetRoomByID(ctx context.Context, roomID uuid.UUID) (*models.Room, error) {
	row := s.DB.QueryRow(ctx, `SELECT `+roomColumns+` FROM rooms WHERE id = $1`, roomID)
	return scanRoom(row)
}

// ListRoomsForUser returns rooms userID is currently an active member of,
// most recently joined first.
func (s *Store) ListRoomsForUser(ctx context.Context, userID uuid.UUID) ([]*models.Room, error) {
	const q = `
		SELECT r.id, r.name, r.type, r.creator_id, r.description, r.avatar_url, r.is_archived, r.created_at, r.updated_at, r.is_public
		FROM rooms r
		JOIN room_members m ON m.room_id = r.id
		WHERE m.user_id = $1 AND m.left_at IS NULL
		ORDER BY m.joined_at DESC`

	rows, err := s.DB.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("querying rooms for user: %w", err)
	}
	defer rows.Close()

	rooms := make([]*models.Room, 0)
	for rows.Next() {
		room, err := scanRoom(rows)
		if err != nil {
			return nil, err
		}
		rooms = append(rooms, room)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating rooms for user: %w", err)
	}

	return rooms, nil
}

// GetMembership returns the caller's active (not left) membership row for a
// room, or ErrNotFound if they aren't currently a member.
func (s *Store) GetMembership(ctx context.Context, roomID, userID uuid.UUID) (*models.RoomMember, error) {
	const q = `
		SELECT id, room_id, user_id, role, joined_at, left_at
		FROM room_members
		WHERE room_id = $1 AND user_id = $2 AND left_at IS NULL`

	var m models.RoomMember
	err := s.DB.QueryRow(ctx, q, roomID, userID).
		Scan(&m.ID, &m.RoomID, &m.UserID, &m.Role, &m.JoinedAt, &m.LeftAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scanning room membership: %w", err)
	}
	return &m, nil
}

// ListMembers returns active members of a room with their public user info,
// oldest membership first.
func (s *Store) ListMembers(ctx context.Context, roomID uuid.UUID) ([]models.RoomMemberWithUser, error) {
	const q = `
		SELECT u.id, u.username, u.avatar_url, m.role, m.joined_at
		FROM room_members m
		JOIN users u ON u.id = m.user_id
		WHERE m.room_id = $1 AND m.left_at IS NULL
		ORDER BY m.joined_at ASC`

	rows, err := s.DB.Query(ctx, q, roomID)
	if err != nil {
		return nil, fmt.Errorf("querying room members: %w", err)
	}
	defer rows.Close()

	members := make([]models.RoomMemberWithUser, 0)
	for rows.Next() {
		var mem models.RoomMemberWithUser
		if err := rows.Scan(&mem.User.ID, &mem.User.Username, &mem.User.AvatarURL, &mem.Role, &mem.JoinedAt); err != nil {
			return nil, fmt.Errorf("scanning room member: %w", err)
		}
		members = append(members, mem)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating room members: %w", err)
	}

	return members, nil
}

// AddMember adds userID to roomID with the given role. If userID previously
// left the room, they're reactivated (fresh joined_at, left_at cleared, role
// updated to the given role). If userID is already an active member,
// ErrAlreadyMember is returned.
func (s *Store) AddMember(ctx context.Context, roomID, userID uuid.UUID, role models.MemberRole) (*models.RoomMember, error) {
	const q = `
		INSERT INTO room_members (id, room_id, user_id, role)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (room_id, user_id) DO UPDATE
			SET role = EXCLUDED.role, joined_at = NOW(), left_at = NULL
			WHERE room_members.left_at IS NOT NULL
		RETURNING id, room_id, user_id, role, joined_at, left_at`

	var m models.RoomMember
	err := s.DB.QueryRow(ctx, q, uuid.New(), roomID, userID, role).
		Scan(&m.ID, &m.RoomID, &m.UserID, &m.Role, &m.JoinedAt, &m.LeftAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAlreadyMember
		}
		return nil, fmt.Errorf("adding room member: %w", err)
	}

	return &m, nil
}

// RemoveMember marks userID as having left roomID. Returns ErrNotFound if
// they weren't currently an active member.
func (s *Store) RemoveMember(ctx context.Context, roomID, userID uuid.UUID) error {
	const q = `
		UPDATE room_members
		SET left_at = NOW()
		WHERE room_id = $1 AND user_id = $2 AND left_at IS NULL`

	tag, err := s.DB.Exec(ctx, q, roomID, userID)
	if err != nil {
		return fmt.Errorf("removing room member: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListPublicChannels returns public, non-archived channels excludeUserID is
// not currently an active member of, each with its active member count,
// newest first. Backs the browse-channels list (GET /rooms/public).
func (s *Store) ListPublicChannels(ctx context.Context, excludeUserID uuid.UUID) ([]*models.PublicChannel, error) {
	const q = `
		SELECT r.id, r.name, r.description, r.avatar_url, r.created_at,
		       COUNT(m.id) FILTER (WHERE m.left_at IS NULL) AS member_count
		FROM rooms r
		LEFT JOIN room_members m ON m.room_id = r.id
		WHERE r.type = 'channel' AND r.is_public AND NOT r.is_archived
		  AND NOT EXISTS (
		      SELECT 1 FROM room_members me
		      WHERE me.room_id = r.id AND me.user_id = $1 AND me.left_at IS NULL
		  )
		GROUP BY r.id
		ORDER BY r.created_at DESC`

	rows, err := s.DB.Query(ctx, q, excludeUserID)
	if err != nil {
		return nil, fmt.Errorf("querying public channels: %w", err)
	}
	defer rows.Close()

	channels := make([]*models.PublicChannel, 0)
	for rows.Next() {
		var c models.PublicChannel
		if err := rows.Scan(&c.ID, &c.Name, &c.Description, &c.AvatarURL, &c.CreatedAt, &c.MemberCount); err != nil {
			return nil, fmt.Errorf("scanning public channel: %w", err)
		}
		channels = append(channels, &c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating public channels: %w", err)
	}

	return channels, nil
}
