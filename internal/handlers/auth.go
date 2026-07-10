package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mubeendevelops/convoy-chat/internal/auth"
	"github.com/mubeendevelops/convoy-chat/internal/httpx"
	"github.com/mubeendevelops/convoy-chat/internal/models"
	"github.com/mubeendevelops/convoy-chat/internal/store"
)

// refreshTokenTTL is the sliding refresh-token lifetime: every rotation
// (POST /auth/refresh) pushes a freshly issued token's expiry another 30
// days out, rather than tracking a separate absolute session cap (see
// plan.md's Phase 3 refresh-token proposal).
const refreshTokenTTL = 30 * 24 * time.Hour

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{3,32}$`)

const maxEmailLen = 255

var (
	errInvalidUsername = errors.New("username must be 3-32 characters and contain only letters, numbers, underscores, or hyphens")
	errEmailTooLong    = errors.New("email is too long")
	errEmailInvalid    = errors.New("email is not a valid address")
)

// validateUsername assumes username has already been trimmed.
func validateUsername(username string) error {
	if !usernamePattern.MatchString(username) {
		return errInvalidUsername
	}
	return nil
}

// validateEmail assumes email has already been trimmed and lowercased.
func validateEmail(email string) error {
	if len(email) > maxEmailLen {
		return errEmailTooLong
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return errEmailInvalid
	}
	return nil
}

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
	Token        string       `json:"token"`
	RefreshToken string       `json:"refresh_token"`
	User         *models.User `json:"user"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type logoutRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// issueRefreshToken generates a new opaque refresh token, persists its hash
// under familyID, and returns the plaintext to hand back to the client —
// the only time that plaintext ever exists outside the client itself.
func issueRefreshToken(ctx context.Context, s *store.Store, userID, familyID uuid.UUID) (string, error) {
	plaintext, hash, err := auth.GenerateRefreshToken()
	if err != nil {
		return "", err
	}
	if _, err := s.CreateRefreshToken(ctx, userID, familyID, hash, time.Now().Add(refreshTokenTTL)); err != nil {
		return "", err
	}
	return plaintext, nil
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

		if err := validateUsername(req.Username); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", err.Error())
			return
		}

		if err := validateEmail(req.Email); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", err.Error())
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

		refreshToken, err := issueRefreshToken(r.Context(), s, user.ID, uuid.New())
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to generate refresh token")
			return
		}

		httpx.WriteJSON(w, http.StatusCreated, authResponse{Token: token, RefreshToken: refreshToken, User: user})
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

		refreshToken, err := issueRefreshToken(r.Context(), s, user.ID, uuid.New())
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to generate refresh token")
			return
		}

		httpx.WriteJSON(w, http.StatusOK, authResponse{Token: token, RefreshToken: refreshToken, User: user})
	}
}

// Refresh handles POST /api/v1/auth/refresh. The presented refresh token is
// itself the credential — no Bearer header is involved, since the entire
// point is to obtain a new access token once the old one has expired.
// Rotates the token (old one revoked, new one issued in the same family);
// see internal/store.RotateRefreshToken for the not-found/reused/expired
// branches, all of which collapse to the same generic 401 here — no
// enumeration of *why* a refresh token was rejected, matching Login's
// "never reveal which check failed" spirit.
func Refresh(s *store.Store, jwtSecret string, jwtTTL time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req refreshRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "request body must be valid JSON")
			return
		}
		if req.RefreshToken == "" {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "refresh_token is required")
			return
		}

		const refreshRejectedMessage = "invalid or expired refresh token"

		newPlaintext, newHash, err := auth.GenerateRefreshToken()
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to generate refresh token")
			return
		}

		oldHash := auth.HashRefreshToken(req.RefreshToken)
		rotated, err := s.RotateRefreshToken(r.Context(), oldHash, newHash, time.Now().Add(refreshTokenTTL))
		if err != nil {
			switch {
			case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrRefreshTokenReused), errors.Is(err, store.ErrTokenExpired):
				httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", refreshRejectedMessage)
			default:
				httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to refresh session")
			}
			return
		}

		user, err := s.GetUserByID(r.Context(), rotated.UserID)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to look up user")
			return
		}

		accessToken, err := auth.GenerateToken(user.ID, jwtSecret, jwtTTL)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to generate token")
			return
		}

		httpx.WriteJSON(w, http.StatusOK, authResponse{Token: accessToken, RefreshToken: newPlaintext, User: user})
	}
}

// Logout handles POST /api/v1/auth/logout (Bearer-protected). Revokes the
// presented refresh token's whole rotation family, ending that session
// chain server-side rather than only discarding the client's local copy.
// A missing/already-revoked token, or one that belongs to a different user
// than the Bearer token's caller, is a no-op 200 rather than an error —
// logging out an already-logged-out (or bogus) session isn't a failure, and
// this also stops the endpoint being usable to revoke *someone else's*
// session just by presenting their token value while authenticated as
// yourself.
func Logout(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		callerID, _ := auth.UserIDFromContext(r.Context())

		var req logoutRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "request body must be valid JSON")
			return
		}

		const loggedOut = "logged_out"
		if req.RefreshToken == "" {
			httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": loggedOut})
			return
		}

		hash := auth.HashRefreshToken(req.RefreshToken)
		rt, err := s.GetRefreshTokenByHash(r.Context(), hash)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": loggedOut})
				return
			}
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to look up refresh token")
			return
		}

		if rt.UserID != callerID {
			httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": loggedOut})
			return
		}

		if err := s.RevokeRefreshTokenFamily(r.Context(), rt.FamilyID); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to revoke session")
			return
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": loggedOut})
	}
}
