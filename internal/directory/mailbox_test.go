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
	for _, dir := range []string{root, filepath.Join(root, "example.com")} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o775 || info.Mode()&os.ModeSetgid == 0 {
			t.Fatalf("%s mode %o", dir, info.Mode())
		}
	}
	if _, err := os.Stat(filepath.Join(root, "example.com", "b", "Maildir", "new")); err != nil {
		t.Fatal(err)
	}
}

func TestCreateInboxRepairsPrivateRoot(t *testing.T) {
	root := t.TempDir()
	blocked := filepath.Join(root, "vhosts")
	if err := os.Mkdir(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	mail := Maildir{Root: blocked, UID: os.Getuid(), GID: os.Getgid()}
	if err := mail.CreateInbox("a@example.com"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(blocked)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o775 || info.Mode()&os.ModeSetgid == 0 {
		t.Fatalf("root mode %o", info.Mode())
	}
	info, err = os.Stat(filepath.Join(blocked, "example.com", "a", "Maildir", "new"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("mailbox mode %o", info.Mode().Perm())
	}
}

func TestCreateInboxRepairsSealedHome(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "example.com", "a")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	// Mode 0 stands in for a home already sealed to another uid: this
	// process cannot create Maildir until it takes the directory back.
	if err := os.Chmod(home, 0); err != nil {
		t.Fatal(err)
	}
	mail := Maildir{Root: root, UID: os.Getuid(), GID: os.Getgid()}
	if err := mail.CreateInbox("a@example.com"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(home, "Maildir", "new"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("mailbox mode %o", info.Mode().Perm())
	}
	info, err = os.Stat(home)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("home mode %o", info.Mode().Perm())
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
