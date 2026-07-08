package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const testSecret = "test-secret-at-least-32-bytes-long!"

func TestGenerateAndValidateToken_RoundTrip(t *testing.T) {
	userID := uuid.New()

	tokenString, err := GenerateToken(userID, testSecret, time.Hour)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	claims, err := ValidateToken(tokenString, testSecret)
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}

	gotID, err := UserIDFromClaims(claims)
	if err != nil {
		t.Fatalf("UserIDFromClaims: %v", err)
	}
	if gotID != userID {
		t.Errorf("got user id %s, want %s", gotID, userID)
	}
}

func TestValidateToken_Expired(t *testing.T) {
	userID := uuid.New()
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			IssuedAt:  jwt.NewNumericDate(now.Add(-2 * time.Hour)),
			ExpiresAt: jwt.NewNumericDate(now.Add(-time.Hour)), // expired an hour ago
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("signing hand-crafted expired token: %v", err)
	}

	if _, err := ValidateToken(signed, testSecret); err == nil {
		t.Fatal("expected an error for an expired token, got nil")
	}
}

func TestValidateToken_WrongSecret(t *testing.T) {
	tokenString, err := GenerateToken(uuid.New(), testSecret, time.Hour)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	if _, err := ValidateToken(tokenString, "a-completely-different-secret-32b"); err == nil {
		t.Fatal("expected an error when validating with the wrong secret, got nil")
	}
}

func TestValidateToken_Garbage(t *testing.T) {
	cases := []string{
		"",
		"not-a-jwt-at-all",
		"still.not.valid",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ4In0.deadbeef", // well-formed shape, bad signature
	}
	for _, tc := range cases {
		if _, err := ValidateToken(tc, testSecret); err == nil {
			t.Errorf("ValidateToken(%q): expected an error, got nil", tc)
		}
	}
}

func TestValidateToken_RejectsUnexpectedSigningMethod(t *testing.T) {
	// A token signed with "none" (or any non-HMAC method) must be rejected
	// even though it's structurally well-formed — this is the classic JWT
	// algorithm-confusion attack ValidateToken's method check guards against.
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   uuid.New().String(),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	signed, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("signing none-algorithm token: %v", err)
	}

	if _, err := ValidateToken(signed, testSecret); err == nil {
		t.Fatal("expected an error for a none-algorithm token, got nil")
	}
}

func TestUserIDFromClaims_InvalidSubject(t *testing.T) {
	claims := &Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "not-a-uuid"}}
	if _, err := UserIDFromClaims(claims); err == nil {
		t.Fatal("expected an error for a non-UUID subject, got nil")
	}
}
