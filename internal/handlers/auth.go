package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/mubeendevelops/convoy-chat/internal/auth"
	"github.com/mubeendevelops/convoy-chat/internal/httpx"
	"github.com/mubeendevelops/convoy-chat/internal/models"
	"github.com/mubeendevelops/convoy-chat/internal/store"
)

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{3,32}$`)

const maxEmailLen = 255

type signupRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type authResponse struct {
	Token string       `json:"token"`
	User  *models.User `json:"user"`
}

// Signup handles POST /api/v1/auth/signup.
func Signup(s *store.Store, jwtSecret string, jwtTTL time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req signupRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "request body must be valid JSON")
			return
		}

		req.Username = strings.TrimSpace(req.Username)
		req.Email = strings.ToLower(strings.TrimSpace(req.Email))

		if !usernamePattern.MatchString(req.Username) {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "username must be 3-32 characters and contain only letters, numbers, underscores, or hyphens")
			return
		}

		if len(req.Email) > maxEmailLen {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "email is too long")
			return
		}
		if _, err := mail.ParseAddress(req.Email); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "email is not a valid address")
			return
		}

		if err := auth.ValidatePassword(req.Password); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", err.Error())
			return
		}

		hash, err := auth.HashPassword(req.Password)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to process password")
			return
		}

		user, err := s.CreateUser(r.Context(), req.Username, req.Email, hash)
		if err != nil {
			switch {
			case errors.Is(err, store.ErrDuplicateUsername):
				httpx.WriteError(w, http.StatusConflict, "conflict", "username is already taken")
			case errors.Is(err, store.ErrDuplicateEmail):
				httpx.WriteError(w, http.StatusConflict, "conflict", "email is already registered")
			default:
				httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to create user")
			}
			return
		}

		token, err := auth.GenerateToken(user.ID, jwtSecret, jwtTTL)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to generate token")
			return
		}

		httpx.WriteJSON(w, http.StatusCreated, authResponse{Token: token, User: user})
	}
}

// Login handles POST /api/v1/auth/login.
func Login(s *store.Store, jwtSecret string, jwtTTL time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req loginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "request body must be valid JSON")
			return
		}

		req.Email = strings.ToLower(strings.TrimSpace(req.Email))
		if req.Email == "" || req.Password == "" {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "email and password are required")
			return
		}

		// Same generic message on every failure path below so we never
		// reveal whether an email is registered.
		const invalidCredentials = "invalid email or password"

		user, err := s.GetUserByEmail(r.Context(), req.Email)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", invalidCredentials)
				return
			}
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to look up user")
			return
		}

		if err := auth.ComparePassword(user.PasswordHash, req.Password); err != nil {
			httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", invalidCredentials)
			return
		}

		token, err := auth.GenerateToken(user.ID, jwtSecret, jwtTTL)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to generate token")
			return
		}

		httpx.WriteJSON(w, http.StatusOK, authResponse{Token: token, User: user})
	}
}
