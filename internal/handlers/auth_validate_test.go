package handlers

import (
	"strings"
	"testing"
)

func TestValidateUsername(t *testing.T) {
	cases := []struct {
		name     string
		username string
		wantErr  bool
	}{
		{"valid", "alice_dev-1", false},
		{"minimum length (3)", "abc", false},
		{"maximum length (32)", "abcdefghijklmnopqrstuvwxyz123456", false},
		{"too short (2)", "ab", true},
		{"too long (33)", "abcdefghijklmnopqrstuvwxyz1234567", true},
		{"empty", "", true},
		{"contains space", "alice dev", true},
		{"contains @", "alice@dev", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateUsername(tc.username)
			if tc.wantErr && err == nil {
				t.Errorf("validateUsername(%q): expected an error, got nil", tc.username)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("validateUsername(%q): unexpected error: %v", tc.username, err)
			}
		})
	}
}

func TestValidateEmail(t *testing.T) {
	cases := []struct {
		name    string
		email   string
		wantErr bool
	}{
		{"valid", "alice@example.com", false},
		{"missing @", "aliceexample.com", true},
		{"empty", "", true},
		{"too long but otherwise well-formed", strings.Repeat("a", maxEmailLen) + "@example.com", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateEmail(tc.email)
			if tc.wantErr && err == nil {
				t.Errorf("validateEmail(%q): expected an error, got nil", tc.email)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("validateEmail(%q): unexpected error: %v", tc.email, err)
			}
		})
	}
}
