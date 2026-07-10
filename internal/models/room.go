package models

import (
	"time"

	"github.com/google/uuid"
)

type RoomType string

const (
	RoomTypeDirect  RoomType = "direct"
	RoomTypeGroup   RoomType = "group"
	RoomTypeChannel RoomType = "channel"
)

type MemberRole string

const (
	RoleAdmin  MemberRole = "admin"
	RoleMember MemberRole = "member"
	RoleGuest  MemberRole = "guest"
)

type Room struct {
	ID          uuid.UUID `json:"id"`
	Name        *string   `json:"name,omitempty"`
	Type        RoomType  `json:"type"`
	CreatorID   uuid.UUID `json:"creator_id"`
	Description *string   `json:"description,omitempty"`
	AvatarURL   *string   `json:"avatar_url,omitempty"`
	IsArchived  bool      `json:"is_archived"`
	// IsPublic only has meaning for a channel room: a public channel is
	// listed by ListPublicChannels and self-joinable via JoinChannel. It's
	// carried but unused on direct/group rooms.
	IsPublic  bool      `json:"is_public"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// RoomMember is a raw room_members row.
type RoomMember struct {
	ID       uuid.UUID  `json:"id"`
	RoomID   uuid.UUID  `json:"room_id"`
	UserID   uuid.UUID  `json:"user_id"`
	Role     MemberRole `json:"role"`
	JoinedAt time.Time  `json:"joined_at"`
	LeftAt   *time.Time `json:"left_at,omitempty"`
}

// RoomMemberWithUser is a member row joined with the user's public summary,
// for member-list responses.
type RoomMemberWithUser struct {
	User     UserSummary `json:"user"`
	Role     MemberRole  `json:"role"`
	JoinedAt time.Time   `json:"joined_at"`
}

// AdminRoomSummary is the system-admin-only "every room" listing shape
// (GET /admin/rooms) — unlike PublicChannel (which only ever surfaces public
// channels to non-members), this includes every room type and visibility,
// since only a system admin can reach it. A purpose-built read shape, not
// the full Room row.
type AdminRoomSummary struct {
	ID          uuid.UUID   `json:"id"`
	Name        *string     `json:"name,omitempty"`
	Type        RoomType    `json:"type"`
	Creator     UserSummary `json:"creator"`
	MemberCount int         `json:"member_count"`
	IsArchived  bool        `json:"is_archived"`
	CreatedAt   time.Time   `json:"created_at"`
}

// PublicChannel is a public, non-archived channel the caller isn't currently
// an active member of, with its active member count — the shape served by
// the browse-channels list (GET /rooms/public). A purpose-built read shape
// rather than the full Room row, same spirit as MessageReactionSummary.
type PublicChannel struct {
	ID          uuid.UUID `json:"id"`
	Name        *string   `json:"name,omitempty"`
	Description *string   `json:"description,omitempty"`
	AvatarURL   *string   `json:"avatar_url,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	MemberCount int       `json:"member_count"`
}
