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
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
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
