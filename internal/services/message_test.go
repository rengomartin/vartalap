package services

import (
	"strings"
	"testing"
)

func TestValidateContent(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"plain", "  hello  ", "hello", false},
		{"multiline", "one\ntwo", "one\ntwo", false},
		{"crlf normalised", "one\r\ntwo", "one\ntwo", false},
		{"blank lines collapsed", "one\n\n\n\ntwo", "one\n\ntwo", false},
		{"empty", "   ", "", true},
		{"too long", strings.Repeat("x", 101), "", true},
		{"null byte", "hel\x00lo", "", true},
		{"vertical tab", "hel\vlo", "", true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, err := ValidateContent(test.input, 100)
			if test.wantErr {
				if err == nil {
					t.Fatalf("ValidateContent(%q) expected an error", test.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateContent(%q): %v", test.input, err)
			}
			if got != test.want {
				t.Fatalf("ValidateContent(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestValidateContentCountsRunes(t *testing.T) {
	input := strings.Repeat("🌐", 50)
	if _, err := ValidateContent(input, 50); err != nil {
		t.Fatalf("50 emoji should fit in 50 runes: %v", err)
	}
	if _, err := ValidateContent(input, 49); err == nil {
		t.Fatal("50 emoji should not fit in 49 runes")
	}
}
