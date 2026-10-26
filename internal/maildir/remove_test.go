package maildir

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveInbox(t *testing.T) {
	root := t.TempDir()
	keep := filepath.Join(root, "marker")
	if err := os.WriteFile(keep, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	ada := filepath.Join(root, "example.com", "ada.probe", "Maildir", "cur", "msg")
	bea := filepath.Join(root, "example.com", "bea.probe", "Maildir", "new")
	if err := os.MkdirAll(filepath.Dir(ada), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ada, []byte("mail"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bea, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "example.com", "ada.probe", "link")); err != nil {
		t.Fatal(err)
	}

	if err := RemoveInbox(root, "ada.probe@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, "example.com", "ada.probe")); !os.IsNotExist(err) {
		t.Fatalf("ada home still present: %v", err)
	}
	if _, err := os.Stat(bea); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(outside)
	if err != nil || string(body) != "secret" {
		t.Fatalf("symlink target = %q, %v", body, err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "example.com")); err != nil {
		t.Fatal(err)
	}

	if err := RemoveInbox(root, "bea.probe@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, "example.com")); !os.IsNotExist(err) {
		t.Fatalf("empty domain still present: %v", err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatal(err)
	}
	if err := RemoveInbox(root, "bea.probe@example.com"); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveInboxRejectsSymlinkHome(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	home := filepath.Join(root, "example.com", "ada.probe")
	if err := os.MkdirAll(filepath.Dir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, home); err != nil {
		t.Fatal(err)
	}
	if err := RemoveInbox(root, "ada.probe@example.com"); err == nil {
		t.Fatal("accepted a symlink home")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveInboxRejectsPathTricks(t *testing.T) {
	root := t.TempDir()
	for _, address := range []string{"a/b@example.com", `a\b@example.com`, "user@../evil", "user@foo/bar", "@example.com", "user@", "..@example.com", "user@.."} {
		if err := RemoveInbox(root, address); err == nil {
			t.Fatalf("accepted %q", address)
		}
	}
}

func TestListenAddr(t *testing.T) {
	got, err := ListenAddr("")
	if err != nil || got != "127.0.0.1:50056" {
		t.Fatalf("default = %q, %v", got, err)
	}
	if _, err := ListenAddr(":50056"); err == nil {
		t.Fatal("accepted a wildcard host")
	}
	if _, err := ListenAddr("0.0.0.0:50056"); err == nil {
		t.Fatal("accepted every interface")
	}
	if _, err := ListenAddr("127.0.0.1:25"); err == nil {
		t.Fatal("accepted a privileged port")
	}
}
