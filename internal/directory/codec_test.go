package directory

import "testing"

func TestBase64RoundTrip(t *testing.T) {
	got, err := DecodeBase64("password", "c2VjcmV0")
	if err != nil {
		t.Fatal(err)
	}
	if got != "secret" {
		t.Fatalf("got %q", got)
	}
}

func TestBase64RejectsGarbage(t *testing.T) {
	if _, err := DecodeBase64("password", "!!!!"); err == nil {
		t.Fatal("expected error")
	}
}

func TestPageToken(t *testing.T) {
	token := EncodePageToken(42)
	id, err := DecodePageToken(token)
	if err != nil {
		t.Fatal(err)
	}
	if id != 42 {
		t.Fatalf("got %d", id)
	}
	id, err = DecodePageToken("  ")
	if err != nil || id != 0 {
		t.Fatalf("empty token: %d %v", id, err)
	}
	if _, err := DecodePageToken("%%%%"); err == nil {
		t.Fatal("expected error")
	}
}
