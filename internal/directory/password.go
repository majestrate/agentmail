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

// VerifyPassword reports whether plain matches a stored BLF-CRYPT hash.
// A mismatch is false with a nil error. A hash that is not BLF-CRYPT is an error.
func VerifyPassword(stored, plain string) (bool, error) {
	if plain == "" || stored == "" {
		return false, nil
	}
	if !strings.HasPrefix(stored, passwordScheme) {
		return false, fmt.Errorf("password hash must use %s", passwordScheme)
	}
	err := bcrypt.CompareHashAndPassword([]byte(strings.TrimPrefix(stored, passwordScheme)), []byte(plain))
	if err == bcrypt.ErrMismatchedHashAndPassword {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
