package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// refreshTokenBytes is the amount of entropy in a generated refresh token
// (256 bits) — comfortably beyond brute-force range, matching the general
// guidance for bearer session tokens.
const refreshTokenBytes = 32

// GenerateRefreshToken returns a new opaque refresh token: plaintext is the
// base64url value handed to the client (and never stored server-side), hash
// is its hex-encoded SHA-256, which is what actually gets persisted in
// refresh_tokens.token_hash. A refresh token is high-entropy random input
// (unlike a user-chosen password), so a fast cryptographic hash is
// sufficient here — there's no offline-guessing risk bcrypt's slowness
// defends against for passwords.
func GenerateRefreshToken() (plaintext, hash string, err error) {
	buf := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("generating refresh token: %w", err)
	}
	plaintext = base64.RawURLEncoding.EncodeToString(buf)
	return plaintext, HashRefreshToken(plaintext), nil
}

// HashRefreshToken hashes a presented plaintext refresh token so it can be
// looked up by (or compared against) refresh_tokens.token_hash without ever
// storing the plaintext itself.
func HashRefreshToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}
