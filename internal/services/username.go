package services

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	MinUsernameLength = 3
	MaxUsernameLength = 24
)

var usernamePattern = regexp.MustCompile(`^[a-z0-9_]+$`)

// reservedUsernames cannot be registered because they collide with routes or
// could be used to impersonate the service.
var reservedUsernames = map[string]struct{}{
	"admin": {}, "administrator": {}, "api": {}, "app": {}, "auth": {},
	"chats": {}, "discover": {}, "help": {}, "login": {}, "logout": {},
	"me": {}, "message": {}, "messages": {}, "new": {}, "profile": {},
	"recover": {}, "recovery": {}, "root": {}, "settings": {}, "signup": {},
	"support": {}, "system": {}, "u": {}, "user": {}, "users": {},
	"vartalap": {}, "ws": {}, "www": {},
}

// NormalizeUsername lowercases and trims a username.
func NormalizeUsername(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

// ValidateUsername normalises and validates a unique username.
func ValidateUsername(raw string) (string, error) {
	name := NormalizeUsername(raw)
	if len(name) < MinUsernameLength {
		return "", fmt.Errorf("username must be at least %d characters", MinUsernameLength)
	}
	if len(name) > MaxUsernameLength {
		return "", fmt.Errorf("username must be at most %d characters", MaxUsernameLength)
	}
	if !usernamePattern.MatchString(name) {
		return "", fmt.Errorf("username may only contain lowercase letters, numbers and underscores")
	}
	if strings.HasPrefix(name, "_") || strings.HasSuffix(name, "_") || strings.Contains(name, "__") {
		return "", fmt.Errorf("username cannot start or end with an underscore or contain two in a row")
	}
	if _, reserved := reservedUsernames[name]; reserved {
		return "", fmt.Errorf("that username is reserved")
	}
	return name, nil
}
