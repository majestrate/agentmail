package maildir

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// RemoveInbox deletes the mailbox home for a canonical address:
// Root/<domain>/<local>, including Maildir. A missing home is success.
// An empty domain directory is removed. The mail root is left in place.
// Symbolic links are unlinked and not followed.
func RemoveInbox(root, address string) error {
	if !filepath.IsAbs(root) {
		return fmt.Errorf("mail root must be absolute")
	}
	domainDir, home, err := mailboxDirs(root, address)
	if err != nil {
		return err
	}
	info, err := os.Lstat(home)
	if os.IsNotExist(err) {
		return removeEmptyDir(domainDir)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("mailbox path is a symlink")
	}
	if !info.IsDir() {
		return fmt.Errorf("mailbox path is not a directory")
	}
	if err := removeTree(home); err != nil {
		return err
	}
	return removeEmptyDir(domainDir)
}

func mailboxDirs(root, address string) (domainDir, home string, err error) {
	local, domain, err := mailboxParts(address)
	if err != nil {
		return "", "", err
	}
	domainDir = filepath.Join(root, domain)
	home = filepath.Join(domainDir, local)
	if err := withinRoot(root, home); err != nil {
		return "", "", err
	}
	if err := withinRoot(root, domainDir); err != nil {
		return "", "", err
	}
	if domainDir == root || home == root || home == domainDir {
		return "", "", fmt.Errorf("address cannot be used as a mailbox path")
	}
	return domainDir, home, nil
}

func removeTree(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return os.Remove(path)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		child, err := childPath(path, entry.Name())
		if err != nil {
			return err
		}
		if err := removeTree(child); err != nil {
			return err
		}
	}
	return os.Remove(path)
}

func childPath(parent, name string) (string, error) {
	if !pathSegment(name) {
		return "", fmt.Errorf("mailbox entry %q", name)
	}
	path := filepath.Join(parent, name)
	if err := withinRoot(parent, path); err != nil {
		return "", err
	}
	return path, nil
}

func removeEmptyDir(path string) error {
	err := os.Remove(path)
	if err == nil || os.IsNotExist(err) {
		return nil
	}
	if errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST) {
		return nil
	}
	return err
}

func mailboxParts(address string) (local, domain string, err error) {
	local, domain, ok := strings.Cut(address, "@")
	if !ok || local == "" || domain == "" || strings.Contains(domain, "@") {
		return "", "", fmt.Errorf("address cannot be used as a mailbox path")
	}
	if !pathSegment(local) || !pathSegment(domain) {
		return "", "", fmt.Errorf("address cannot be used as a mailbox path")
	}
	return local, domain, nil
}

func pathSegment(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	return !strings.ContainsAny(value, `/\`)
}

func withinRoot(root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("address cannot be used as a mailbox path")
	}
	return nil
}
