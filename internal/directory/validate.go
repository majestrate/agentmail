package directory

import (
	"fmt"
	"net/mail"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxDisplayNameLen = 200

// CanonicalAddress validates a mailbox with net/mail and returns it in lowercase.
func CanonicalAddress(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	parsed, err := mail.ParseAddress(trimmed)
	if err != nil {
		return "", fmt.Errorf("address: %w", err)
	}
	if !strings.EqualFold(trimmed, parsed.Address) {
		return "", fmt.Errorf("address must be a bare email address")
	}
	return strings.ToLower(parsed.Address), nil
}

// CleanDisplayName accepts an empty name and rejects control characters.
func CleanDisplayName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if utf8.RuneCountInString(name) > maxDisplayNameLen {
		return "", fmt.Errorf("display_name is too long")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("display_name contains a control character")
		}
	}
	return name, nil
}
