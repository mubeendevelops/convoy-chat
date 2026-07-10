package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mubeendevelops/convoy-chat/internal/auth"
	"github.com/mubeendevelops/convoy-chat/internal/httpx"
	"github.com/mubeendevelops/convoy-chat/internal/models"
	"github.com/mubeendevelops/convoy-chat/internal/store"
)

type membershipContextKey struct{}

// RequireRoomAdmin is chi middleware gating a route on the caller being an
// active admin of the room named by the URL's {room_id} param — for new
// admin-only endpoints (role changes, and the future kick endpoint) rather
// than another inline `if membership.Role != admin` copy. A room that
// doesn't exist and a room the caller isn't an admin of both 403 identically
// (same masking idiom as GetRoom/requireActiveMembership); a direct room has
// no admin member at all, so this naturally rejects every caller on one
// without a type-specific check, same as InviteMember's existing rule.
// Deliberately not retrofitted onto InviteMember/DeleteMessage's existing
// inline checks — those work fine today and aren't worth the churn. On
// success the resolved membership is injected into the request context via
// MembershipFromContext so the handler doesn't need to re-fetch it.
func RequireRoomAdmin(s *store.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, _ := auth.UserIDFromContext(r.Context())

			roomID, err := uuid.Parse(chi.URLParam(r, "room_id"))
			if err != nil {
				httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "room_id must be a valid UUID")
				return
			}

			membership, err := s.GetMembership(r.Context(), roomID, userID)
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					httpx.WriteError(w, http.StatusForbidden, "forbidden", "you are not an admin of this room")
					return
				}
				httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to check membership")
				return
			}
			if membership.Role != models.RoleAdmin {
				httpx.WriteError(w, http.StatusForbidden, "forbidden", "you are not an admin of this room")
				return
			}

			ctx := context.WithValue(r.Context(), membershipContextKey{}, membership)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// MembershipFromContext returns the caller's *models.RoomMember injected by
// RequireRoomAdmin.
func MembershipFromContext(ctx context.Context) (*models.RoomMember, bool) {
	m, ok := ctx.Value(membershipContextKey{}).(*models.RoomMember)
	return m, ok
}
