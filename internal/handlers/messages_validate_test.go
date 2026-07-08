package handlers

import (
	"strings"
	"testing"
	"time"

	"github.com/mubeendevelops/convoy-chat/internal/models"
)

func TestValidateMessageContent(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wantErr bool
	}{
		{"valid", "hello", false},
		{"minimum length (1)", "x", false},
		{"maximum length (10000)", strings.Repeat("x", 10000), false},
		{"empty", "", true},
		{"too long (10001)", strings.Repeat("x", 10001), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateMessageContent(tc.content)
			if tc.wantErr && err == nil {
				t.Errorf("validateMessageContent: expected an error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("validateMessageContent: unexpected error: %v", err)
			}
		})
	}
}

func TestNormalizeMessageType(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    models.MessageType
		wantErr bool
	}{
		{"empty defaults to text", "", models.MessageTypeText, false},
		{"text", "text", models.MessageTypeText, false},
		{"image", "image", models.MessageTypeImage, false},
		{"file", "file", models.MessageTypeFile, false},
		{"system is rejected as client input", "system", "", true},
		{"unknown type", "bogus", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeMessageType(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Errorf("normalizeMessageType(%q): expected an error, got nil", tc.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeMessageType(%q): unexpected error: %v", tc.raw, err)
			}
			if got != tc.want {
				t.Errorf("normalizeMessageType(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestParseMessageLimit(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    int
		wantErr bool
	}{
		{"empty defaults", "", defaultMessageLimit, false},
		{"valid", "10", 10, false},
		{"minimum (1)", "1", 1, false},
		{"maximum (100)", "100", 100, false},
		{"zero", "0", 0, true},
		{"too large (101)", "101", 0, true},
		{"negative", "-1", 0, true},
		{"not a number", "abc", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseMessageLimit(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Errorf("parseMessageLimit(%q): expected an error, got nil", tc.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseMessageLimit(%q): unexpected error: %v", tc.raw, err)
			}
			if got != tc.want {
				t.Errorf("parseMessageLimit(%q) = %d, want %d", tc.raw, got, tc.want)
			}
		})
	}
}

func TestParseMessageBefore(t *testing.T) {
	t.Run("empty means not provided", func(t *testing.T) {
		got, err := parseMessageBefore("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})

	t.Run("valid RFC3339", func(t *testing.T) {
		const raw = "2026-07-05T15:30:00Z"
		got, err := parseMessageBefore(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want, _ := time.Parse(time.RFC3339Nano, raw)
		if got == nil || !got.Equal(want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("malformed timestamp", func(t *testing.T) {
		if _, err := parseMessageBefore("not-a-timestamp"); err == nil {
			t.Error("expected an error, got nil")
		}
	})
}
