package auth

import (
	"strings"
	"testing"
)

func TestHashAndComparePassword_RoundTrip(t *testing.T) {
	const password = "CorrectHorse123"

	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if hash == password {
		t.Fatal("hash must not equal the plaintext password")
	}

	if err := ComparePassword(hash, password); err != nil {
		t.Errorf("ComparePassword with the correct password: %v", err)
	}
	if err := ComparePassword(hash, "WrongPassword123"); err == nil {
		t.Error("ComparePassword with the wrong password: expected an error, got nil")
	}
}

func TestValidatePassword(t *testing.T) {
	cases := []struct {
		name    string
		pw      string
		wantErr bool
	}{
		{"valid minimum length", "Abcdefg1", false},
		{"valid long", strings.Repeat("a", 69) + "Bc1", false}, // 72 bytes exactly
		{"too short (7 chars)", "Abcdef1", true},
		{"too long (73 bytes)", strings.Repeat("a", 69) + "Bc12", true},
		{"missing uppercase", "abcdefg1", true},
		{"missing lowercase", "ABCDEFG1", true},
		{"missing digit", "Abcdefgh", true},
		{"empty", "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePassword(tc.pw)
			if tc.wantErr && err == nil {
				t.Errorf("ValidatePassword(%q): expected an error, got nil", tc.pw)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("ValidatePassword(%q): unexpected error: %v", tc.pw, err)
			}
		})
	}
}
