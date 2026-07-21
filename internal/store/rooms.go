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

// CreateGroup creates a named, always-private multi-person room and adds
// creatorID as its admin plus every memberID as a plain member, atomically.
// Mirrors CreateChannel's shape; is_public is always false for a group (never
// browsable/self-joinable — ListPublicChannels/JoinChannel are scoped to
// type='channel' already). Callers are expected to have already validated
// memberIDs (real users, no self-reference, deduped) — this method trusts
// its input the same way insertMember does.
func (s *Store) CreateGroup(ctx context.Context, creatorID uuid.UUID, name string, description *string, memberIDs []uuid.UUID) (*models.Room, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	room := &models.Room{
		ID:          uuid.New(),
		Name:        &name,
		Type:        models.RoomTypeGroup,
		CreatorID:   creatorID,
		Description: description,
	}

	const insertRoomStmt = `
		INSERT INTO rooms (id, name, type, creator_id, description, is_public)
		VALUES ($1, $2, $3, $4, $5, false)
		RETURNING is_archived, is_public, created_at, updated_at`

	err = tx.QueryRow(ctx, insertRoomStmt, room.ID, room.Name, room.Type, room.CreatorID, room.Description).
		Scan(&room.IsArchived, &room.IsPublic, &room.CreatedAt, &room.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("inserting room: %w", err)
	}

	if err := insertMember(ctx, tx, room.ID, creatorID, models.RoleAdmin); err != nil {
		return nil, err
	}
	for _, memberID := range memberIDs {
		if err := insertMember(ctx, tx, room.ID, memberID, models.RoleMember); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	return room, nil
}

// ChangeMemberRole sets userID's role within roomID to newRole. changed is
// false (no error) when the member already held newRole — a no-op success,
// same "nothing changed, nothing to announce" idiom as MarkMessageRead, so
// the caller knows not to broadcast member.role_changed. Demoting the room's
// last remaining active admin to member is rejected with ErrLastAdmin — a
// deliberate demote is different from a departure (see
// PromoteOldestIfNoAdmins), so it doesn't get an auto-succession path; the
// caller can promote someone else first. Runs inside a transaction with a
// row lock on the admin-count check to stay race-safe under concurrent
// demotes of the same room's admins.
func (s *Store) ChangeMemberRole(ctx context.Context, roomID, userID uuid.UUID, newRole models.MemberRole) (changed bool, err error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const selectStmt = `
		SELECT role FROM room_members
		WHERE room_id = $1 AND user_id = $2 AND left_at IS NULL
		FOR UPDATE`

	var currentRole models.MemberRole
	err = tx.QueryRow(ctx, selectStmt, roomID, userID).Scan(&currentRole)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, ErrNotFound
		}
		return false, fmt.Errorf("looking up member role: %w", err)
	}

	if currentRole == newRole {
		return false, nil
	}

	if currentRole == models.RoleAdmin && newRole != models.RoleAdmin {
		const countAdminsStmt = `
			SELECT COUNT(*) FROM room_members
			WHERE room_id = $1 AND role = 'admin' AND left_at IS NULL`
		var adminCount int
		if err := tx.QueryRow(ctx, countAdminsStmt, roomID).Scan(&adminCount); err != nil {
			return false, fmt.Errorf("counting room admins: %w", err)
		}
		if adminCount <= 1 {
			return false, ErrLastAdmin
		}
	}

	const updateStmt = `
		UPDATE room_members SET role = $3
		WHERE room_id = $1 AND user_id = $2 AND left_at IS NULL`
	if _, err := tx.Exec(ctx, updateStmt, roomID, userID, newRole); err != nil {
		return false, fmt.Errorf("updating member role: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("committing transaction: %w", err)
	}
	return true, nil
}

// PromoteOldestIfNoAdmins checks whether roomID currently has zero active
// admins and, if so, promotes its longest-tenured remaining active member
// (oldest joined_at, id as a stable tiebreak) to admin. Returns the promoted
// member, or nil if no promotion was needed or possible (an admin still
// remains, the room's type has no admin concept — gated to 'channel'/'group'
// so a 'direct' room's participants are never touched, preserving the
// invariant that DM members are always plain 'member' — or no members are
// left at all). One atomic statement, run by the caller immediately after a
// departure (LeaveRoom today; a future kick reuses this too, though kicking
// can never actually zero out admins since the kicker themselves is always
// an admin who isn't removing themselves).
//
// Accepted race (not fixed here): two members of the same admin-less-about-
// to-happen room departing in the same instant could each independently
// evaluate "no admin exists" against their own snapshot and each promote a
// candidate — over-promotion (two new admins instead of one), not data
// corruption or lockout. Rare and low-harm enough that a pg_advisory_xact_lock
// isn't justified here.
func (s *Store) PromoteOldestIfNoAdmins(ctx context.Context, roomID uuid.UUID) (*models.RoomMember, error) {
	const q = `
		WITH admin_exists AS (
			SELECT 1 FROM room_members
			WHERE room_id = $1 AND role = 'admin' AND left_at IS NULL
			LIMIT 1
		),
		room_check AS (
			SELECT 1 FROM rooms WHERE id = $1 AND type IN ('channel', 'group')
		),
		candidate AS (
			SELECT id FROM room_members
			WHERE room_id = $1 AND left_at IS NULL
			  AND EXISTS (SELECT 1 FROM room_check)
			  AND NOT EXISTS (SELECT 1 FROM admin_exists)
			ORDER BY joined_at ASC, id ASC
			LIMIT 1
		)
		UPDATE room_members
		SET role = 'admin'
		WHERE id IN (SELECT id FROM candidate)
		RETURNING id, room_id, user_id, role, joined_at, left_at`

	var m models.RoomMember
	err := s.DB.QueryRow(ctx, q, roomID).Scan(&m.ID, &m.RoomID, &m.UserID, &m.Role, &m.JoinedAt, &m.LeftAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("promoting oldest member: %w", err)
	}
	return &m, nil
}

// ListAllRooms returns every room in the system, regardless of the caller's
// own membership, newest first — for the system-admin dashboard
// (GET /admin/rooms). Plain offset pagination, not keyset: a deliberate
// deviation from the message-history precedent, since this is a low-traffic,
// human-paged admin screen rather than a live-scrolling feed (see plan.md's
// admin-dashboard proposal).
func (s *Store) ListAllRooms(ctx context.Context, limit, offset int) ([]models.AdminRoomSummary, error) {
	const q = `
		SELECT r.id, r.name, r.type, r.is_archived, r.created_at,
		       u.id, u.username, u.avatar_url,
		       COUNT(m.id) FILTER (WHERE m.left_at IS NULL) AS member_count
		FROM rooms r
		JOIN users u ON u.id = r.creator_id
		LEFT JOIN room_members m ON m.room_id = r.id
		GROUP BY r.id, u.id
		ORDER BY r.created_at DESC
		LIMIT $1 OFFSET $2`

	rows, err := s.DB.Query(ctx, q, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("querying all rooms: %w", err)
	}
	defer rows.Close()

	rooms := make([]models.AdminRoomSummary, 0)
	for rows.Next() {
		var rm models.AdminRoomSummary
		err := rows.Scan(
			&rm.ID, &rm.Name, &rm.Type, &rm.IsArchived, &rm.CreatedAt,
			&rm.Creator.ID, &rm.Creator.Username, &rm.Creator.AvatarURL, &rm.MemberCount,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning admin room summary: %w", err)
		}
		rooms = append(rooms, rm)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating all rooms: %w", err)
	}

	return rooms, nil
}

// GetOrCreateDirectRoom returns the existing direct room between userA and
// userB, or creates one if none exists yet. created is true only when a new
// room row was inserted; peerReactivated is true when userB specifically
// (conventionally the "peer" from the caller/userA's point of view — see
// handlers.CreateRoom) had left and was just reactivated, which the caller
// needs to know since — unlike userA, who learns the outcome synchronously
// from this call's own return — userB's own client has no other way to find
// out. A Postgres advisory lock keyed on the sorted user pair serializes
// concurrent attempts so two simultaneous requests can't create two separate
// direct rooms for the same pair.
//
// The existence check matches on the pair having ever had membership rows in
// a direct room together, regardless of left_at — not "both currently
// active". A direct room structurally can only ever hold these exact two
// users (nothing can invite a third person into one — see CLAUDE.md), so
// this can't accidentally match a room that's grown or shrunk to a different
// pair. Requiring both active (the original check) meant that once either
// side left — "Leave conversation" is an intentional, labeled action for DMs,
// not a mistake, see RoomHeader.tsx — this lookup stopped finding the room
// at all: the leaver's next attempt to message the same peer forked a brand
// new, disconnected room instead of resuming the old one, orphaning it with
// the peer still active and sending into a conversation nobody could ever
// see again. Any side found with left_at set is reactivated in place
// (mirroring AddMember's reactivation upsert), so the SAME room and its full
// history come back for whoever left.
func (s *Store) GetOrCreateDirectRoom(ctx context.Context, userA, userB uuid.UUID) (room *models.Room, created, peerReactivated bool, err error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, false, false, fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const lockStmt = `SELECT pg_advisory_xact_lock(hashtextextended(least($1::text, $2::text) || ':' || greatest($1::text, $2::text), 0))`
	if _, err := tx.Exec(ctx, lockStmt, userA, userB); err != nil {
		return nil, false, false, fmt.Errorf("acquiring direct-room lock: %w", err)
	}

	const findStmt = `
		SELECT ` + roomColumns + `
		FROM rooms r
		WHERE r.type = 'direct'
		  AND EXISTS (SELECT 1 FROM room_members WHERE room_id = r.id AND user_id = $1)
		  AND EXISTS (SELECT 1 FROM room_members WHERE room_id = r.id AND user_id = $2)
		ORDER BY r.created_at ASC
		LIMIT 1`

	room, err = scanRoom(tx.QueryRow(ctx, findStmt, userA, userB))
	if err == nil {
		const reactivateStmt = `
			UPDATE room_members
			SET left_at = NULL, joined_at = NOW()
			WHERE room_id = $1 AND user_id = ANY($2) AND left_at IS NOT NULL
			RETURNING user_id`
		rows, qErr := tx.Query(ctx, reactivateStmt, room.ID, []uuid.UUID{userA, userB})
		if qErr != nil {
			return nil, false, false, fmt.Errorf("reactivating direct room membership: %w", qErr)
		}
		reactivatedB := false
		for rows.Next() {
			var reactivatedUserID uuid.UUID
			if scanErr := rows.Scan(&reactivatedUserID); scanErr != nil {
				rows.Close()
				return nil, false, false, fmt.Errorf("scanning reactivated direct room member: %w", scanErr)
			}
			if reactivatedUserID == userB {
				reactivatedB = true
			}
		}
		rows.Close()
		if rowsErr := rows.Err(); rowsErr != nil {
			return nil, false, false, fmt.Errorf("iterating reactivated direct room members: %w", rowsErr)
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, false, false, fmt.Errorf("committing transaction: %w", err)
		}
		return room, false, reactivatedB, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, false, false, err
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
		return nil, false, false, fmt.Errorf("inserting direct room: %w", err)
	}

	if err := insertMember(ctx, tx, newRoom.ID, userA, models.RoleMember); err != nil {
		return nil, false, false, err
	}
	if err := insertMember(ctx, tx, newRoom.ID, userB, models.RoleMember); err != nil {
		return nil, false, false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, false, false, fmt.Errorf("committing transaction: %w", err)
	}

	return newRoom, true, false, nil
}

func (s *Store) GetRoomByID(ctx context.Context, roomID uuid.UUID) (*models.Room, error) {
	row := s.DB.QueryRow(ctx, `SELECT `+roomColumns+` FROM rooms WHERE id = $1`, roomID)
	return scanRoom(row)
}

// ListRoomsForUser returns rooms userID is currently an active member of,
// most recently joined first. Each room carries UnreadCount: the number of
// messages newer than the member's last-read cursor (COALESCEd to joined_at
// when never opened), excluding the caller's own and deleted messages. This
// query has its own scan rather than reusing scanRoom, since it selects the
// extra unread_count column.
func (s *Store) ListRoomsForUser(ctx context.Context, userID uuid.UUID) ([]*models.Room, error) {
	const q = `
		SELECT r.id, r.name, r.type, r.creator_id, r.description, r.avatar_url, r.is_archived, r.created_at, r.updated_at, r.is_public,
		       (SELECT COUNT(*) FROM messages msg
		         WHERE msg.room_id = r.id
		           AND msg.deleted_at IS NULL
		           AND msg.user_id <> $1
		           AND msg.created_at > COALESCE(m.last_read_at, m.joined_at)) AS unread_count
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
		var rm models.Room
		if err := rows.Scan(&rm.ID, &rm.Name, &rm.Type, &rm.CreatorID, &rm.Description, &rm.AvatarURL, &rm.IsArchived, &rm.CreatedAt, &rm.UpdatedAt, &rm.IsPublic, &rm.UnreadCount); err != nil {
			return nil, fmt.Errorf("scanning room for user: %w", err)
		}
		rooms = append(rooms, &rm)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating rooms for user: %w", err)
	}

	return rooms, nil
}

// AdvanceLastRead stamps userID's last-read cursor in roomID to NOW(), marking
// every message currently in the room as read for them — called when they open
// the room (POST /rooms/{id}/read). Returns ErrNotFound if they aren't an
// active member. Mirrors RemoveMember's WHERE left_at IS NULL / zero-rows idiom.
func (s *Store) AdvanceLastRead(ctx context.Context, roomID, userID uuid.UUID) error {
	const q = `
		UPDATE room_members
		SET last_read_at = NOW()
		WHERE room_id = $1 AND user_id = $2 AND left_at IS NULL`

	tag, err := s.DB.Exec(ctx, q, roomID, userID)
	if err != nil {
		return fmt.Errorf("advancing last-read cursor: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
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
// ErrAlreadyMember is returned. This is also the ban-lifting path: an admin
// re-inviting a previously-kicked user clears banned_at, since inviting is a
// deliberate "let them back in" action (a banned user's own self-join is
// blocked separately, by JoinChannel's IsBanned pre-check — see CLAUDE.md).
func (s *Store) AddMember(ctx context.Context, roomID, userID uuid.UUID, role models.MemberRole) (*models.RoomMember, error) {
	const q = `
		INSERT INTO room_members (id, room_id, user_id, role)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (room_id, user_id) DO UPDATE
			SET role = EXCLUDED.role, joined_at = NOW(), left_at = NULL, banned_at = NULL
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
// they weren't currently an active member. Used for a self-leave (leaving is
// not a ban); a kick uses BanMember instead.
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

// BanMember marks userID as having left roomID *and* bans them, so they can't
// self-join back in (JoinChannel's IsBanned pre-check blocks it) — used by the
// admin kick path. An admin re-invite (AddMember) lifts the ban. Returns
// ErrNotFound if they weren't currently an active member. Mirrors
// RemoveMember's WHERE left_at IS NULL idiom.
func (s *Store) BanMember(ctx context.Context, roomID, userID uuid.UUID) error {
	const q = `
		UPDATE room_members
		SET left_at = NOW(), banned_at = NOW()
		WHERE room_id = $1 AND user_id = $2 AND left_at IS NULL`

	tag, err := s.DB.Exec(ctx, q, roomID, userID)
	if err != nil {
		return fmt.Errorf("banning room member: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// IsBanned reports whether userID has a banned row in roomID (a kicked member
// who hasn't been re-invited). Backs JoinChannel's self-join pre-check.
func (s *Store) IsBanned(ctx context.Context, roomID, userID uuid.UUID) (bool, error) {
	const q = `
		SELECT EXISTS (
			SELECT 1 FROM room_members
			WHERE room_id = $1 AND user_id = $2 AND banned_at IS NOT NULL
		)`

	var banned bool
	if err := s.DB.QueryRow(ctx, q, roomID, userID).Scan(&banned); err != nil {
		return false, fmt.Errorf("checking member ban: %w", err)
	}
	return banned, nil
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
		  AND NOT EXISTS (
		      SELECT 1 FROM room_members b
		      WHERE b.room_id = r.id AND b.user_id = $1 AND b.banned_at IS NOT NULL
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
