package directory

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreateInbox(t *testing.T) {
	root := t.TempDir()
	mail := Maildir{Root: root, UID: os.Getuid(), GID: os.Getgid()}
	if err := mail.CreateInbox("a.user@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := mail.CreateInbox("b@example.com"); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"cur", "new", "tmp"} {
		info, err := os.Stat(filepath.Join(root, "example.com", "a.user", "Maildir", sub))
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() {
			t.Fatalf("%s is not a directory", sub)
		}
		if info.Mode().Perm() != 0o700 {
			t.Fatalf("%s mode %o", sub, info.Mode().Perm())
		}
	}
	info, err := os.Stat(filepath.Join(root, "example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o775 {
		t.Fatalf("domain mode %o", info.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(root, "example.com", "b", "Maildir", "new")); err != nil {
		t.Fatal(err)
	}
}

func TestCreateInboxRejectsPathTricks(t *testing.T) {
	mail := Maildir{Root: t.TempDir(), UID: os.Getuid(), GID: os.Getgid()}
	for _, address := range []string{"a/b@example.com", `a\b@example.com`, "user@../evil", "user@foo/bar", "@example.com", "user@", "..@example.com"} {
		if err := mail.CreateInbox(address); err == nil {
			t.Fatalf("accepted %q", address)
		}
	}
}
