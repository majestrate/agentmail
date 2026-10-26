package directory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Maildir creates Dovecot Maildir inboxes under Root.
// Layout matches the Dovecot image: Root/<domain>/<local>/Maildir/{cur,new,tmp}.
type Maildir struct {
	Root string
	UID  int
	GID  int
}

// CreateInbox creates the inbox directories for a canonical address.
// The domain directory stays group-writable so later users in that domain
// can be added. The mailbox itself is private to UID/GID.
func (m Maildir) CreateInbox(address string) error {
	if !filepath.IsAbs(m.Root) {
		return fmt.Errorf("mail root must be absolute")
	}
	local, domain, err := mailboxParts(address)
	if err != nil {
		return err
	}
	domainDir := filepath.Join(m.Root, domain)
	home := filepath.Join(domainDir, local)
	maildir := filepath.Join(home, "Maildir")
	if err := withinRoot(m.Root, maildir); err != nil {
		return err
	}

	if err := os.MkdirAll(m.Root, 0o755); err != nil {
		return err
	}
	domainCreated := false
	if _, err := os.Stat(domainDir); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		domainCreated = true
	}
	if err := os.MkdirAll(domainDir, 0o2775); err != nil {
		return err
	}
	if domainCreated {
		if err := os.Chmod(domainDir, 0o2775); err != nil {
			return err
		}
		if err := os.Chown(domainDir, m.UID, m.GID); err != nil {
			return err
		}
	}

	dirs := []string{home, maildir, filepath.Join(maildir, "cur"), filepath.Join(maildir, "new"), filepath.Join(maildir, "tmp")}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
		if err := os.Chown(dir, m.UID, m.GID); err != nil {
			return err
		}
	}
	return nil
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
