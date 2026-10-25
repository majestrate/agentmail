package directory

import (
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

const passwordScheme = "{BLF-CRYPT}"

// HashPassword returns a Dovecot BLF-CRYPT hash. The plaintext is not stored.
func HashPassword(plain string) (string, error) {
	if plain == "" {
		return "", fmt.Errorf("password is required")
	}
	if strings.ContainsAny(plain, "\r\n") {
		return "", fmt.Errorf("password must be a single line")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return passwordScheme + string(hash), nil
}
