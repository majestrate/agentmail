package directory

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestHashPassword(t *testing.T) {
	hash, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "{BLF-CRYPT}$2") {
		t.Fatalf("hash %q", hash)
	}
	stored := strings.TrimPrefix(hash, "{BLF-CRYPT}")
	if err := bcrypt.CompareHashAndPassword([]byte(stored), []byte("correct horse")); err != nil {
		t.Fatal(err)
	}
	if _, err := HashPassword(""); err == nil {
		t.Fatal("accepted empty password")
	}
}
