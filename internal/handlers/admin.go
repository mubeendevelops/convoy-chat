package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/mubeendevelops/convoy-chat/internal/httpx"
	"github.com/mubeendevelops/convoy-chat/internal/store"
)

const (
	defaultAdminRoomsLimit = 50
	maxAdminRoomsLimit     = 200
)

var (
	errInvalidAdminLimit  = errors.New("limit must be an integer between 1 and 200")
	errInvalidAdminOffset = errors.New("offset must be a non-negative integer")
)

// parseAdminLimit parses the ?limit= query param, defaulting to
// defaultAdminRoomsLimit when raw is empty. Mirrors parseMessageLimit's
// shape (messages.go).
func parseAdminLimit(raw string) (int, error) {
	if raw == "" {
		return defaultAdminRoomsLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maxAdminRoomsLimit {
		return 0, errInvalidAdminLimit
	}
	return n, nil
}

// parseAdminOffset parses the ?offset= query param, defaulting to 0.
func parseAdminOffset(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, errInvalidAdminOffset
	}
	return n, nil
}

// ListAllRooms handles GET /api/v1/admin/rooms?limit=&offset= — every room
// in the system, regardless of the caller's own membership. Gated by
// RequireSystemAdmin (registered in the router).
func ListAllRooms(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, err := parseAdminLimit(r.URL.Query().Get("limit"))
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", err.Error())
			return
		}
		offset, err := parseAdminOffset(r.URL.Query().Get("offset"))
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", err.Error())
			return
		}

		rooms, err := s.ListAllRooms(r.Context(), limit, offset)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to list rooms")
			return
		}

		httpx.WriteJSON(w, http.StatusOK, rooms)
	}
}

// ListAllUserPresence handles GET /api/v1/admin/presence — every registered
// user's current presence status. Gated by RequireSystemAdmin.
func ListAllUserPresence(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entries, err := s.ListAllUserPresence(r.Context())
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to list presence")
			return
		}

		httpx.WriteJSON(w, http.StatusOK, entries)
	}
}
