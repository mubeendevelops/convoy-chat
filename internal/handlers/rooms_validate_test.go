package handlers

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestValidateRoomName(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid", "general", false},
		{"minimum length (1)", "x", false},
		{"maximum length (255)", strings.Repeat("x", 255), false},
		{"empty", "", true},
		{"too long (256)", strings.Repeat("x", 256), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRoomName(tc.input)
			if tc.wantErr && err == nil {
				t.Errorf("validateRoomName(%q): expected an error, got nil", tc.input)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("validateRoomName(%q): unexpected error: %v", tc.input, err)
			}
		})
	}
}

func TestValidatePeerUserID(t *testing.T) {
	caller := uuid.New()
	other := uuid.New()

	t.Run("valid peer", func(t *testing.T) {
		got, err := validatePeerUserID(other.String(), caller)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != other {
			t.Errorf("got %s, want %s", got, other)
		}
	})

	t.Run("malformed uuid", func(t *testing.T) {
		if _, err := validatePeerUserID("not-a-uuid", caller); err == nil {
			t.Error("expected an error, got nil")
		}
	})

	t.Run("self dm rejected", func(t *testing.T) {
		_, err := validatePeerUserID(caller.String(), caller)
		if err == nil {
			t.Fatal("expected an error, got nil")
		}
		if !errors.Is(err, errSelfDirect) {
			t.Errorf("got error %v, want errSelfDirect", err)
		}
	})
}
