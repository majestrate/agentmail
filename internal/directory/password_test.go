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
	ok, err := VerifyPassword(hash, "correct horse")
	if err != nil || !ok {
		t.Fatalf("verify: ok=%v err=%v", ok, err)
	}
	ok, err = VerifyPassword(hash, "wrong")
	if err != nil || ok {
		t.Fatalf("mismatch: ok=%v err=%v", ok, err)
	}
	if _, err := VerifyPassword("plain", "correct horse"); err == nil {
		t.Fatal("accepted a hash without the BLF-CRYPT scheme")
	}
}
