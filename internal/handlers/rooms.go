package handlers

import (
	"encoding/json"
	"errors"
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

var (
	errInvalidRoomName   = errors.New("name is required and must be 1-255 characters")
	errInvalidPeerUserID = errors.New("peer_user_id must be a valid UUID")
	errSelfDirect        = errors.New("cannot create a direct room with yourself")
	errInvalidRoomType   = errors.New(`type must be "channel" or "direct"`)
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
// requested 403 behavior without leaking which room IDs exist).
func GetRoom(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, _ := auth.UserIDFromContext(r.Context())

		roomID, _, ok := requireActiveMembership(w, r, s, userID)
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

// ListRoomMembers handles GET /api/v1/rooms/{room_id}/members.
func ListRoomMembers(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, _ := auth.UserIDFromContext(r.Context())

		roomID, _, ok := requireActiveMembership(w, r, s, userID)
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

// LeaveRoom handles POST /api/v1/rooms/{room_id}/leave.
func LeaveRoom(s *store.Store) http.HandlerFunc {
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

		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "left"})
	}
}
