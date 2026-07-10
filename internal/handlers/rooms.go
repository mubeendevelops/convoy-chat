package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mubeendevelops/convoy-chat/internal/auth"
	"github.com/mubeendevelops/convoy-chat/internal/httpx"
	"github.com/mubeendevelops/convoy-chat/internal/models"
	"github.com/mubeendevelops/convoy-chat/internal/store"
)

const maxRoomNameLen = 255

const minGroupMembers = 2

var (
	errInvalidRoomName    = errors.New("name is required and must be 1-255 characters")
	errInvalidPeerUserID  = errors.New("peer_user_id must be a valid UUID")
	errSelfDirect         = errors.New("cannot create a direct room with yourself")
	errInvalidRoomType    = errors.New(`type must be "channel", "direct", or "group"`)
	errTooFewGroupMembers = fmt.Errorf("member_ids must include at least %d other user(s)", minGroupMembers)
	errSelfInGroupMembers = errors.New("member_ids must not include yourself")
	errInvalidRole        = errors.New(`role must be "admin" or "member"`)
)

// validateRoomName assumes name has already been trimmed.
func validateRoomName(name string) error {
	if name == "" || len(name) > maxRoomNameLen {
		return errInvalidRoomName
	}
	return nil
}

// validatePeerUserID parses raw and rejects a self-DM, both checked before
// any store call so a malformed or self-targeting request never reaches it.
func validatePeerUserID(raw string, callerID uuid.UUID) (uuid.UUID, error) {
	peerID, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, errInvalidPeerUserID
	}
	if peerID == callerID {
		return uuid.Nil, errSelfDirect
	}
	return peerID, nil
}

type createRoomRequest struct {
	Type        string  `json:"type"`
	Name        string  `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	PeerUserID  string  `json:"peer_user_id,omitempty"`
	// IsPublic only applies to type "channel"; omitted (nil) defaults to
	// true (public/browsable). An explicit false creates a private,
	// invite-only channel — the pre-Phase-2 behavior, opted into.
	IsPublic *bool `json:"is_public,omitempty"`
	// MemberIDs only applies to type "group": the initial member list beyond
	// the creator (who's always the admin). A group is always is_public=false
	// — never browsable/self-joinable, unlike a channel.
	MemberIDs []string `json:"member_ids,omitempty"`
}

// validateGroupMemberIDs parses and validates a group creation's member_ids:
// every entry must be a well-formed UUID, none may be the creator themselves
// (a likely client bug worth surfacing, unlike a duplicate — which is just
// silently deduped), and at least minGroupMembers distinct others are
// required so "group" doesn't become a worse-UX path to what a direct room's
// auto-dedup already handles better for a real 1:1.
func validateGroupMemberIDs(raw []string, creatorID uuid.UUID) ([]uuid.UUID, error) {
	seen := make(map[uuid.UUID]bool, len(raw))
	ids := make([]uuid.UUID, 0, len(raw))
	for _, r := range raw {
		id, err := uuid.Parse(r)
		if err != nil {
			return nil, errors.New("member_ids must be valid UUIDs")
		}
		if id == creatorID {
			return nil, errSelfInGroupMembers
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) < minGroupMembers {
		return nil, errTooFewGroupMembers
	}
	return ids, nil
}

type roomDetailResponse struct {
	*models.Room
	Members []models.RoomMemberWithUser `json:"members"`
}

// requireActiveMembership parses room_id from the URL and verifies the
// caller has an active membership in it, writing the appropriate error
// response if not. ok is false if a response has already been written and
// the caller should return immediately.
func requireActiveMembership(w http.ResponseWriter, r *http.Request, s *store.Store, userID uuid.UUID) (roomID uuid.UUID, membership *models.RoomMember, ok bool) {
	roomID, err := uuid.Parse(chi.URLParam(r, "room_id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "room_id must be a valid UUID")
		return uuid.Nil, nil, false
	}

	membership, err = s.GetMembership(r.Context(), roomID, userID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httpx.WriteError(w, http.StatusForbidden, "forbidden", "you are not a member of this room")
		} else {
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to check membership")
		}
		return uuid.Nil, nil, false
	}

	return roomID, membership, true
}

// requireMemberOrSystemAdmin behaves like requireActiveMembership, except a
// system admin bypasses the active-membership requirement entirely — used
// only by GetRoom/ListRoomMembers (read-only visibility into any room), NOT
// ListMessages/SendMessage/InviteMember/etc., which stay room-membership-
// only. A system admin's power deliberately doesn't extend to messaging or
// membership management in rooms they don't belong to (see plan.md's
// admin-dashboard proposal for the full scope boundary).
func requireMemberOrSystemAdmin(w http.ResponseWriter, r *http.Request, s *store.Store, userID uuid.UUID) (roomID uuid.UUID, ok bool) {
	roomID, err := uuid.Parse(chi.URLParam(r, "room_id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "room_id must be a valid UUID")
		return uuid.Nil, false
	}

	if _, err := s.GetMembership(r.Context(), roomID, userID); err == nil {
		return roomID, true
	} else if !errors.Is(err, store.ErrNotFound) {
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to check membership")
		return uuid.Nil, false
	}

	caller, err := s.GetUserByID(r.Context(), userID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to check admin status")
		return uuid.Nil, false
	}
	if !caller.IsSystemAdmin {
		httpx.WriteError(w, http.StatusForbidden, "forbidden", "you are not a member of this room")
		return uuid.Nil, false
	}

	return roomID, true
}

// CreateRoom handles POST /api/v1/rooms. type "channel" creates a named
// room with the caller as admin; type "direct" gets-or-creates the 1:1 room
// with peer_user_id (201 if newly created, 200 if it already existed).
func CreateRoom(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, _ := auth.UserIDFromContext(r.Context())

		var req createRoomRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "request body must be valid JSON")
			return
		}

		switch req.Type {
		case "channel":
			name := strings.TrimSpace(req.Name)
			if err := validateRoomName(name); err != nil {
				httpx.WriteError(w, http.StatusBadRequest, "invalid_input", err.Error())
				return
			}

			isPublic := true
			if req.IsPublic != nil {
				isPublic = *req.IsPublic
			}

			room, err := s.CreateChannel(r.Context(), userID, name, req.Description, isPublic)
			if err != nil {
				httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to create room")
				return
			}

			httpx.WriteJSON(w, http.StatusCreated, room)

		case "direct":
			peerID, err := validatePeerUserID(req.PeerUserID, userID)
			if err != nil {
				httpx.WriteError(w, http.StatusBadRequest, "invalid_input", err.Error())
				return
			}

			if _, err := s.GetUserByID(r.Context(), peerID); err != nil {
				if errors.Is(err, store.ErrNotFound) {
					httpx.WriteError(w, http.StatusNotFound, "not_found", "peer user not found")
					return
				}
				httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to look up peer user")
				return
			}

			room, created, err := s.GetOrCreateDirectRoom(r.Context(), userID, peerID)
			if err != nil {
				httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to create direct room")
				return
			}

			status := http.StatusOK
			if created {
				status = http.StatusCreated
			}
			httpx.WriteJSON(w, status, room)

		case "group":
			name := strings.TrimSpace(req.Name)
			if err := validateRoomName(name); err != nil {
				httpx.WriteError(w, http.StatusBadRequest, "invalid_input", err.Error())
				return
			}

			memberIDs, err := validateGroupMemberIDs(req.MemberIDs, userID)
			if err != nil {
				httpx.WriteError(w, http.StatusBadRequest, "invalid_input", err.Error())
				return
			}

			for _, memberID := range memberIDs {
				if _, err := s.GetUserByID(r.Context(), memberID); err != nil {
					if errors.Is(err, store.ErrNotFound) {
						httpx.WriteError(w, http.StatusNotFound, "not_found", "one or more member_ids not found")
						return
					}
					httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to look up member")
					return
				}
			}

			room, err := s.CreateGroup(r.Context(), userID, name, req.Description, memberIDs)
			if err != nil {
				httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to create room")
				return
			}

			httpx.WriteJSON(w, http.StatusCreated, room)

		default:
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", errInvalidRoomType.Error())
		}
	}
}

// ListRooms handles GET /api/v1/rooms.
func ListRooms(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, _ := auth.UserIDFromContext(r.Context())

		rooms, err := s.ListRoomsForUser(r.Context(), userID)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to list rooms")
			return
		}

		httpx.WriteJSON(w, http.StatusOK, rooms)
	}
}

// GetRoom handles GET /api/v1/rooms/{room_id}: details + member list, 403
// if the caller isn't an active member (also covers a nonexistent room,
// since it's then indistinguishable from "not a member" — this matches the
// requested 403 behavior without leaking which room IDs exist). A system
// admin bypasses the membership requirement (see requireMemberOrSystemAdmin).
func GetRoom(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, _ := auth.UserIDFromContext(r.Context())

		roomID, ok := requireMemberOrSystemAdmin(w, r, s, userID)
		if !ok {
			return
		}

		room, err := s.GetRoomByID(r.Context(), roomID)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to look up room")
			return
		}

		members, err := s.ListMembers(r.Context(), roomID)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to list members")
			return
		}

		httpx.WriteJSON(w, http.StatusOK, roomDetailResponse{Room: room, Members: members})
	}
}

// ListRoomMembers handles GET /api/v1/rooms/{room_id}/members. A system
// admin bypasses the membership requirement (see requireMemberOrSystemAdmin).
func ListRoomMembers(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, _ := auth.UserIDFromContext(r.Context())

		roomID, ok := requireMemberOrSystemAdmin(w, r, s, userID)
		if !ok {
			return
		}

		members, err := s.ListMembers(r.Context(), roomID)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to list members")
			return
		}

		httpx.WriteJSON(w, http.StatusOK, members)
	}
}

type inviteRequest struct {
	UserID string `json:"user_id"`
}

// InviteMember handles POST /api/v1/rooms/{room_id}/invite. Admin-only.
// Direct rooms have no admin member, so this naturally rejects invites to
// them with the same 403 as any other non-admin caller.
func InviteMember(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, _ := auth.UserIDFromContext(r.Context())

		roomID, membership, ok := requireActiveMembership(w, r, s, userID)
		if !ok {
			return
		}
		if membership.Role != models.RoleAdmin {
			httpx.WriteError(w, http.StatusForbidden, "forbidden", "only room admins can invite members")
			return
		}

		var req inviteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "request body must be valid JSON")
			return
		}

		inviteeID, err := uuid.Parse(req.UserID)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "user_id must be a valid UUID")
			return
		}

		if _, err := s.GetUserByID(r.Context(), inviteeID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				httpx.WriteError(w, http.StatusNotFound, "not_found", "user not found")
				return
			}
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to look up user")
			return
		}

		member, err := s.AddMember(r.Context(), roomID, inviteeID, models.RoleMember)
		if err != nil {
			if errors.Is(err, store.ErrAlreadyMember) {
				httpx.WriteError(w, http.StatusConflict, "conflict", "user is already a member of this room")
				return
			}
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to add member")
			return
		}

		httpx.WriteJSON(w, http.StatusCreated, member)
	}
}

// ListPublicChannels handles GET /api/v1/rooms/public: public, non-archived
// channels the caller isn't currently an active member of, with member
// counts, for the browse-channels UI.
func ListPublicChannels(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, _ := auth.UserIDFromContext(r.Context())

		channels, err := s.ListPublicChannels(r.Context(), userID)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to list public channels")
			return
		}

		httpx.WriteJSON(w, http.StatusOK, channels)
	}
}

// joinedEvent matches the WS "user.joined" shape from CLAUDE.md:
// {"type":"user.joined","user":{"id","username"},"room_id"}. Defined here
// rather than internal/websocket for the same reason reactionEvent is in
// reactions.go — publishing only needs store.PublishRoomEvent, not the
// Hub/Broker.
type joinedEvent struct {
	Type   string     `json:"type"`
	User   joinedUser `json:"user"`
	RoomID uuid.UUID  `json:"room_id"`
}

type joinedUser struct {
	ID       uuid.UUID `json:"id"`
	Username string    `json:"username"`
}

// JoinChannel handles POST /api/v1/rooms/{room_id}/join: the caller adds
// *themselves* as a member of a public channel — distinct from the
// admin-only invite. A room that doesn't exist, isn't a channel, isn't
// public, or is archived all produce the same 403, mirroring GetRoom's
// "403 masks nonexistent-vs-forbidden" idiom so probing room IDs can't tell
// the two apart. Already being an active member is a 409, same as invite.
func JoinChannel(s *store.Store, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, _ := auth.UserIDFromContext(r.Context())

		roomID, err := uuid.Parse(chi.URLParam(r, "room_id"))
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "room_id must be a valid UUID")
			return
		}

		room, err := s.GetRoomByID(r.Context(), roomID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				httpx.WriteError(w, http.StatusForbidden, "forbidden", "this room isn't a joinable public channel")
				return
			}
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to look up room")
			return
		}
		if room.Type != models.RoomTypeChannel || !room.IsPublic || room.IsArchived {
			httpx.WriteError(w, http.StatusForbidden, "forbidden", "this room isn't a joinable public channel")
			return
		}

		member, err := s.AddMember(r.Context(), roomID, userID, models.RoleMember)
		if err != nil {
			if errors.Is(err, store.ErrAlreadyMember) {
				httpx.WriteError(w, http.StatusConflict, "conflict", "you are already a member of this room")
				return
			}
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to join room")
			return
		}

		// Membership is already committed at this point, so a broadcast
		// hiccup shouldn't fail the request back to the caller — logged, not
		// fatal, same philosophy as ToggleReaction's publish step.
		if joiner, err := s.GetUserByID(r.Context(), userID); err != nil {
			logger.Error("looking up joining user failed", "user_id", userID, "room_id", roomID, "error", err)
		} else {
			payload, err := json.Marshal(joinedEvent{
				Type:   "user.joined",
				User:   joinedUser{ID: joiner.ID, Username: joiner.Username},
				RoomID: roomID,
			})
			if err != nil {
				logger.Error("marshaling user.joined event failed", "room_id", roomID, "error", err)
			} else if err := s.PublishRoomEvent(r.Context(), roomID, payload); err != nil {
				logger.Warn("publishing user.joined event failed", "room_id", roomID, "error", err)
			}
		}

		httpx.WriteJSON(w, http.StatusCreated, member)
	}
}

// leftEvent matches the WS "user.left" shape from CLAUDE.md:
// {"type":"user.left","user_id","room_id"}. Defined here rather than
// internal/websocket for the same reason joinedEvent/reactionEvent are —
// publishing only needs store.PublishRoomEvent, not the Hub/Broker.
type leftEvent struct {
	Type   string    `json:"type"`
	UserID uuid.UUID `json:"user_id"`
	RoomID uuid.UUID `json:"room_id"`
}

// memberRoleChangedEvent matches the new WS "member.role_changed" shape:
// {"type","room_id","user_id","role"} — fired by both an explicit
// PATCH .../role change and a leave-triggered auto-promotion
// (PromoteOldestIfNoAdmins), so an already-open members list updates a
// badge live and the affected user's own client can unlock/lock
// admin-only controls without a refetch.
type memberRoleChangedEvent struct {
	Type   string            `json:"type"`
	RoomID uuid.UUID         `json:"room_id"`
	UserID uuid.UUID         `json:"user_id"`
	Role   models.MemberRole `json:"role"`
}

// publishLeftEvent and publishRoleChangedEvent are shared by LeaveRoom and
// RemoveMember (kick) below — both trigger the same "someone's active
// membership just ended" / "someone's role just changed" broadcasts.
func publishLeftEvent(ctx context.Context, s *store.Store, logger *slog.Logger, roomID, userID uuid.UUID) {
	payload, err := json.Marshal(leftEvent{Type: "user.left", UserID: userID, RoomID: roomID})
	if err != nil {
		logger.Error("marshaling user.left event failed", "room_id", roomID, "user_id", userID, "error", err)
		return
	}
	if err := s.PublishRoomEvent(ctx, roomID, payload); err != nil {
		logger.Warn("publishing user.left event failed", "room_id", roomID, "user_id", userID, "error", err)
	}
}

func publishRoleChangedEvent(ctx context.Context, s *store.Store, logger *slog.Logger, roomID, userID uuid.UUID, role models.MemberRole) {
	payload, err := json.Marshal(memberRoleChangedEvent{Type: "member.role_changed", RoomID: roomID, UserID: userID, Role: role})
	if err != nil {
		logger.Error("marshaling member.role_changed event failed", "room_id", roomID, "user_id", userID, "error", err)
		return
	}
	if err := s.PublishRoomEvent(ctx, roomID, payload); err != nil {
		logger.Warn("publishing member.role_changed event failed", "room_id", roomID, "user_id", userID, "error", err)
	}
}

// LeaveRoom handles POST /api/v1/rooms/{room_id}/leave. On success, publishes
// user.left (closing a pre-existing gap: this endpoint used to publish
// nothing, so other members only ever saw a departure via the WS room.leave
// side-channel or their next refetch) and, if the leaver was the room's last
// active admin, runs PromoteOldestIfNoAdmins and publishes
// member.role_changed for whoever got promoted.
func LeaveRoom(s *store.Store, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, _ := auth.UserIDFromContext(r.Context())

		roomID, err := uuid.Parse(chi.URLParam(r, "room_id"))
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "room_id must be a valid UUID")
			return
		}

		if err := s.RemoveMember(r.Context(), roomID, userID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				httpx.WriteError(w, http.StatusNotFound, "not_found", "you are not a member of this room")
				return
			}
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to leave room")
			return
		}

		// Membership is already committed at this point, so broadcast hiccups
		// shouldn't fail the request back to the caller — logged, not fatal,
		// same philosophy as ToggleReaction's publish step.
		publishLeftEvent(r.Context(), s, logger, roomID, userID)

		promoted, err := s.PromoteOldestIfNoAdmins(r.Context(), roomID)
		if err != nil {
			logger.Error("admin-succession check failed", "room_id", roomID, "error", err)
		} else if promoted != nil {
			publishRoleChangedEvent(r.Context(), s, logger, roomID, promoted.UserID, promoted.Role)
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "left"})
	}
}

type changeRoleRequest struct {
	Role string `json:"role"`
}

// ChangeMemberRole handles PATCH /api/v1/rooms/{room_id}/members/{user_id}/role.
// Gated by RequireRoomAdmin (registered in the router), so a caller reaching
// this handler is already confirmed to be an active admin of the room —
// including the DM case, which RequireRoomAdmin already rejects since a
// direct room has no admin member at all. A request that changes nothing
// (target already holds the requested role) succeeds as a no-op without
// broadcasting; demoting the room's last remaining admin 409s
// (store.ErrLastAdmin) rather than auto-succeeding — that's what leaving is
// for.
func ChangeMemberRole(s *store.Store, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		roomID, err := uuid.Parse(chi.URLParam(r, "room_id"))
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "room_id must be a valid UUID")
			return
		}
		targetID, err := uuid.Parse(chi.URLParam(r, "user_id"))
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "user_id must be a valid UUID")
			return
		}

		var req changeRoleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "request body must be valid JSON")
			return
		}

		var newRole models.MemberRole
		switch req.Role {
		case string(models.RoleAdmin):
			newRole = models.RoleAdmin
		case string(models.RoleMember):
			newRole = models.RoleMember
		default:
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", errInvalidRole.Error())
			return
		}

		changed, err := s.ChangeMemberRole(r.Context(), roomID, targetID, newRole)
		if err != nil {
			switch {
			case errors.Is(err, store.ErrNotFound):
				httpx.WriteError(w, http.StatusNotFound, "not_found", "user is not an active member of this room")
			case errors.Is(err, store.ErrLastAdmin):
				httpx.WriteError(w, http.StatusConflict, "conflict", "cannot demote the room's last remaining admin")
			default:
				httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to change member role")
			}
			return
		}

		if changed {
			publishRoleChangedEvent(r.Context(), s, logger, roomID, targetID, newRole)
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "changed", "role": string(newRole)})
	}
}

// RemoveMember handles DELETE /api/v1/rooms/{room_id}/members/{user_id} —
// kicking a member out of a room. Gated by RequireRoomAdmin. Self-removal
// via this endpoint is rejected (400): that's what POST .../leave is for,
// and LeaveRoom's own admin-succession handling only applies to a genuine
// departure, not a kick. Because the caller is always an admin who isn't
// removing themselves here, a kick can never zero out a room's admins —
// unlike LeaveRoom, this never needs PromoteOldestIfNoAdmins.
func RemoveMember(s *store.Store, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		callerID, _ := auth.UserIDFromContext(r.Context())

		roomID, err := uuid.Parse(chi.URLParam(r, "room_id"))
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "room_id must be a valid UUID")
			return
		}
		targetID, err := uuid.Parse(chi.URLParam(r, "user_id"))
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "user_id must be a valid UUID")
			return
		}
		if targetID == callerID {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "use POST .../leave to remove yourself")
			return
		}

		if err := s.RemoveMember(r.Context(), roomID, targetID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				httpx.WriteError(w, http.StatusNotFound, "not_found", "user is not an active member of this room")
				return
			}
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to remove member")
			return
		}

		publishLeftEvent(r.Context(), s, logger, roomID, targetID)

		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "removed"})
	}
}
