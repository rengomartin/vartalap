// Package services contains validation and identity rules independent of HTTP and storage.
package services

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"
)

const (
	// DeviceIDCookieName is the name of the persistent anonymous device cookie.
	// It is a transport/session identifier, NOT the cryptographic identity.
	DeviceIDCookieName  = "vartalap_id"
	MaxDisplayNameRunes = 48
)

// NewDeviceID generates a random UUID v4 for anonymous device identification.
func NewDeviceID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate device id: %w", err)
	}
	raw[6] = (raw[6] & 0x0f) | 0x40 // version 4
	raw[8] = (raw[8] & 0x3f) | 0x80 // variant 10xx
	return fmt.Sprintf("%x-%x-%x-%x-%x",
		raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16]), nil
}

// IsValidDeviceID reports whether value is a canonical UUID v4 string.
func IsValidDeviceID(value string) bool {
	return IsValidUUID(value)
}

// IsValidUUID reports whether value is a canonical UUID v4 string.
func IsValidUUID(value string) bool {
	_, err := parseDeviceID(value)
	return err == nil
}

func parseDeviceID(value string) ([16]byte, error) {
	var raw [16]byte
	if len(value) != 36 {
		return raw, fmt.Errorf("device id must be 36 characters")
	}
	for _, position := range []int{8, 13, 18, 23} {
		if value[position] != '-' {
			return raw, fmt.Errorf("device id has invalid hyphen at %d", position)
		}
	}
	compact := value[:8] + value[9:13] + value[14:18] + value[19:23] + value[24:]
	decoded, err := hex.DecodeString(compact)
	if err != nil || len(decoded) != 16 {
		return raw, fmt.Errorf("device id is not valid hex")
	}
	copy(raw[:], decoded)
	if raw[6]>>4 != 4 || raw[8]>>6 != 0b10 {
		return raw, fmt.Errorf("device id is not a version 4 UUID")
	}
	return raw, nil
}

// ValidateDisplayName normalises and validates a chosen display name.
func ValidateDisplayName(raw string) (string, error) {
	name := strings.Join(strings.Fields(raw), " ")
	runes := []rune(name)
	if len(runes) == 0 {
		return "", fmt.Errorf("choose a display name")
	}
	if len(runes) > MaxDisplayNameRunes {
		return "", fmt.Errorf("display name must be at most %d characters", MaxDisplayNameRunes)
	}
	for _, r := range runes {
		if unicode.IsControl(r) || r == '\u200b' || r == '\u200e' || r == '\u200f' {
			return "", fmt.Errorf("display name contains invalid characters")
		}
	}
	return name, nil
}

// ValidateDeviceName normalises an optional human label for a device.
func ValidateDeviceName(raw string) (string, error) {
	name := strings.Join(strings.Fields(raw), " ")
	if name == "" {
		return "This device", nil
	}
	runes := []rune(name)
	if len(runes) > 40 {
		return "", fmt.Errorf("device name must be at most 40 characters")
	}
	for _, r := range runes {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("device name contains invalid characters")
		}
	}
	return name, nil
}
