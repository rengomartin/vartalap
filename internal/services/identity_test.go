package services

import (
	"strings"
	"testing"
)

func TestNewDeviceID(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 200; i++ {
		id, err := NewDeviceID()
		if err != nil {
			t.Fatalf("NewDeviceID: %v", err)
		}
		if !IsValidDeviceID(id) {
			t.Fatalf("generated invalid device ID %q", id)
		}
		if seen[id] {
			t.Fatalf("duplicate device ID %q", id)
		}
		seen[id] = true
	}
}

func TestIsValidDeviceID(t *testing.T) {
	cases := []struct {
		name  string
		value string
		valid bool
	}{
		{"valid", "550e8400-e29b-41d4-a716-446655440000", true},
		{"wrong version", "550e8400-e29b-11d4-a716-446655440000", false},
		{"wrong variant", "550e8400-e29b-41d4-2716-446655440000", false},
		{"missing hyphens", "550e8400e29b41d4a716446655440000", false},
		{"truncated", "550e8400-e29b-41d4-a716", false},
		{"garbage", "not-a-uuid-at-all-not-a-uuid-at", false},
		{"empty", "", false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := IsValidDeviceID(test.value); got != test.valid {
				t.Fatalf("IsValidDeviceID(%q) = %v, want %v", test.value, got, test.valid)
			}
		})
	}
}

func TestValidateDisplayName(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"trims and collapses spaces", "  Aap   ji  ", "Aap ji", false},
		{"unicode name", "राहुल", "राहुल", false},
		{"single character", "A", "A", false},
		{"empty", "   ", "", true},
		{"too long", strings.Repeat("a", MaxDisplayNameRunes+1), "", true},
		{"control character", "bad\x01name", "", true},
		{"zero width", "bad\u200bname", "", true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, err := ValidateDisplayName(test.input)
			if test.wantErr {
				if err == nil {
					t.Fatalf("ValidateDisplayName(%q) expected an error", test.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateDisplayName(%q): %v", test.input, err)
			}
			if got != test.want {
				t.Fatalf("ValidateDisplayName(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}
