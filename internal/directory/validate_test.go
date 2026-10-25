package directory

import "testing"

func TestCanonicalAddress(t *testing.T) {
	got, err := CanonicalAddress("  Ada@Example.COM ")
	if err != nil {
		t.Fatal(err)
	}
	if got != "ada@example.com" {
		t.Fatalf("got %q", got)
	}
	for _, bad := range []string{"", "nope", "Ada <ada@example.com>", "a b@example.com"} {
		if _, err := CanonicalAddress(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestCleanDisplayName(t *testing.T) {
	got, err := CleanDisplayName("  Ada Lovelace ")
	if err != nil || got != "Ada Lovelace" {
		t.Fatalf("got %q %v", got, err)
	}
	if _, err := CleanDisplayName("line\nbreak"); err == nil {
		t.Fatal("accepted newline")
	}
}
