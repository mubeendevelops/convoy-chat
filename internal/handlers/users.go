package handlers

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mubeendevelops/convoy-chat/internal/httpx"
	"github.com/mubeendevelops/convoy-chat/internal/store"
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
