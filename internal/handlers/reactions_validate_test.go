package handlers

import (
	"strings"
	"testing"
)

func TestValidateEmoji(t *testing.T) {
	cases := []struct {
		name    string
		emoji   string
		wantErr bool
	}{
		{"valid single emoji", "👍", false},
		{"maximum length (10 bytes)", strings.Repeat("x", 10), false},
		{"empty", "", true},
		{"too long (11 bytes)", strings.Repeat("x", 11), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateEmoji(tc.emoji)
			if tc.wantErr && err == nil {
				t.Errorf("validateEmoji(%q): expected an error, got nil", tc.emoji)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("validateEmoji(%q): unexpected error: %v", tc.emoji, err)
			}
		})
	}
}
