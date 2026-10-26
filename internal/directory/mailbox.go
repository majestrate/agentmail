package directory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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

	// The mail root is shared with Dovecot, which may have created it mode 0700.
	// Group vmail has to be able to walk in and add the next domain.
	if err := ensureShared(m.Root, m.UID, m.GID); err != nil {
		return err
	}
	if err := ensureShared(domainDir, m.UID, m.GID); err != nil {
		return err
	}

	// Build the whole Maildir before handing any of it to the mail uid.
	// Mode 0700 owned by that uid cannot be entered by uid 5002, so sealing
	// the home first makes the next mkdir fail.
	dirs := []string{home, maildir, filepath.Join(maildir, "cur"), filepath.Join(maildir, "new"), filepath.Join(maildir, "tmp")}
	for _, dir := range dirs {
		if err := mkdirMailbox(dir, home); err != nil {
			return err
		}
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := os.Chmod(dirs[i], 0o700); err != nil {
			return err
		}
		if err := os.Chown(dirs[i], m.UID, m.GID); err != nil {
			return fmt.Errorf("chown %s: %w", dirs[i], err)
		}
	}
	return nil
}

// ensureShared makes path a setgid directory the mail group can extend.
// It ends owned by uid/gid. A directory this process cannot chmod is taken
// with CAP_CHOWN, widened, then given to the mail user.
func ensureShared(path string, uid, gid int) error {
	return ensureMode(path, uid, gid, os.ModeSetgid|0o775)
}

// mkdirMailbox creates path. A home left mode 0700 and owned by the mail uid
// is taken back with CAP_CHOWN so the missing Maildir pieces can be added.
// Nothing above home is changed.
func mkdirMailbox(path, home string) error {
	err := os.MkdirAll(path, 0o700)
	if err == nil {
		return nil
	}
	if !errors.Is(err, syscall.EPERM) && !errors.Is(err, syscall.EACCES) {
		return err
	}
	dir := path
	for {
		if dir != home {
			if err := withinRoot(home, dir); err != nil {
				return err
			}
		}
		_, statErr := os.Stat(dir)
		if statErr != nil && !os.IsNotExist(statErr) && !errors.Is(statErr, syscall.EPERM) && !errors.Is(statErr, syscall.EACCES) {
			return statErr
		}
		if statErr == nil {
			if chownErr := os.Chown(dir, os.Getuid(), os.Getgid()); chownErr != nil {
				return fmt.Errorf("chown %s: %w", dir, chownErr)
			}
			if chmodErr := os.Chmod(dir, 0o700); chmodErr != nil {
				return chmodErr
			}
			err = os.MkdirAll(path, 0o700)
			if err == nil {
				return nil
			}
			if !errors.Is(err, syscall.EPERM) && !errors.Is(err, syscall.EACCES) {
				return err
			}
		}
		if dir == home {
			return err
		}
		dir = filepath.Dir(dir)
	}
}

func ensureMode(path string, uid, gid int, mode os.FileMode) error {
	if err := os.MkdirAll(path, mode); err != nil {
		return err
	}
	if err := os.Chmod(path, mode); err != nil {
		if !errors.Is(err, syscall.EPERM) && !errors.Is(err, syscall.EACCES) {
			return err
		}
		if err := os.Chown(path, os.Getuid(), os.Getgid()); err != nil {
			return err
		}
		if err := os.Chmod(path, mode); err != nil {
			return err
		}
	}
	if err := os.Chown(path, uid, gid); err != nil {
		return fmt.Errorf("chown %s: %w", path, err)
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
