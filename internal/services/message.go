package services

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

var repeatedNewlines = regexp.MustCompile(`\n{3,}`)

// ValidateContent normalises and validates chat message text.
func ValidateContent(raw string, maxRunes int) (string, error) {
	content := strings.ReplaceAll(raw, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	content = strings.TrimSpace(content)
	if content == "" {
		return "", fmt.Errorf("message cannot be empty")
	}
	if runes := []rune(content); len(runes) > maxRunes {
		return "", fmt.Errorf("message must be at most %d characters", maxRunes)
	}
	for _, r := range content {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return "", fmt.Errorf("message contains invalid characters")
		}
	}
	return repeatedNewlines.ReplaceAllString(content, "\n\n"), nil
}
