package store

import "errors"

var (
	ErrNotFound          = errors.New("not found")
	ErrDuplicateUsername = errors.New("username already exists")
	ErrDuplicateEmail    = errors.New("email already exists")
	ErrAlreadyMember     = errors.New("already a member")
	// ErrRefreshTokenReused is returned by RotateRefreshToken when the
	// presented token hash matches a row that's already revoked — i.e. a
	// replay of a token that was already rotated past. The caller should
	// revoke the whole token family, not just reject the one request.
	ErrRefreshTokenReused = errors.New("refresh token already used")
	ErrTokenExpired       = errors.New("token expired")
)
