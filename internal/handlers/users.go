package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mubeendevelops/convoy-chat/internal/auth"
	"github.com/mubeendevelops/convoy-chat/internal/httpx"
	"github.com/mubeendevelops/convoy-chat/internal/models"
	"github.com/mubeendevelops/convoy-chat/internal/store"
)

const (
	// userSearchLimit caps how many matches a single search returns — enough
	// for an autocomplete picker, bounded so a broad prefix can't return the
	// whole user table.
	userSearchLimit = 20
	// maxUserSearchQueryLen bounds the query. Usernames are at most 32 chars
	// (CLAUDE.md), so anything longer can't prefix-match a real username; the
	// cap just rejects abuse rather than running a doomed query.
	maxUserSearchQueryLen = 64
)

// GetUser handles GET /api/v1/users/{user_id}. Mounted behind the auth
// middleware: any authenticated user may look up any other user's profile.
func GetUser(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(chi.URLParam(r, "user_id"))
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "user_id must be a valid UUID")
			return
		}

		user, err := s.GetUserByID(r.Context(), id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				httpx.WriteError(w, http.StatusNotFound, "not_found", "user not found")
				return
			}
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to look up user")
			return
		}

		httpx.WriteJSON(w, http.StatusOK, user)
	}
}

// SearchUsers handles GET /api/v1/users/search?q=<prefix>&room_id=<uuid>.
// Behind the auth middleware: any authenticated user may search the directory
// (same exposure as GetUser, which already returns any user by ID). Returns
// UserSummary matches by username prefix, always excluding the caller and —
// when room_id is supplied — anyone already in that room, so an invite picker
// only shows people it can add. An empty query returns [] rather than 400, so
// a debounced picker clearing its input isn't an error.
func SearchUsers(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, _ := auth.UserIDFromContext(r.Context())

		query := strings.TrimSpace(r.URL.Query().Get("q"))
		if query == "" {
			httpx.WriteJSON(w, http.StatusOK, []models.UserSummary{})
			return
		}
		if len(query) > maxUserSearchQueryLen {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "search query is too long")
			return
		}

		var excludeRoomID *uuid.UUID
		if raw := strings.TrimSpace(r.URL.Query().Get("room_id")); raw != "" {
			roomID, err := uuid.Parse(raw)
			if err != nil {
				httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "room_id must be a valid UUID")
				return
			}
			excludeRoomID = &roomID
		}

		users, err := s.SearchUsers(r.Context(), query, userID, excludeRoomID, userSearchLimit)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to search users")
			return
		}

		httpx.WriteJSON(w, http.StatusOK, users)
	}
}
