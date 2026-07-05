package auth

import (
	"errors"
	"fmt"
	"unicode"

	"golang.org/x/crypto/bcrypt"
)

const (
	minPasswordLen = 8
	maxPasswordLen = 72 // bcrypt's input limit is 72 bytes
)

var ErrInvalidPassword = errors.New("password does not meet requirements")

// ValidatePassword enforces the v1 password policy: 8-72 bytes, containing
// at least one uppercase letter, one lowercase letter, and one digit.
func ValidatePassword(password string) error {
	if len(password) < minPasswordLen || len(password) > maxPasswordLen {
		return fmt.Errorf("%w: must be between %d and %d characters", ErrInvalidPassword, minPasswordLen, maxPasswordLen)
	}

	var hasUpper, hasLower, hasDigit bool
	for _, r := range password {
		switch {
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsDigit(r):
			hasDigit = true
		}
	}

	if !hasUpper || !hasLower || !hasDigit {
		return fmt.Errorf("%w: must contain an uppercase letter, a lowercase letter, and a digit", ErrInvalidPassword)
	}

	return nil
}

func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hashing password: %w", err)
	}
	return string(hash), nil
}

// ComparePassword returns nil if password matches hash, and an error
// otherwise (including bcrypt.ErrMismatchedHashAndPassword).
func ComparePassword(hash, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}
