package directory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"strings"

	directoryv1 "agentmail/gen/agentmail/directory/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeUsers struct {
	user    User
	delErr  error
	deleted bool
}

func (f *fakeUsers) GetUser(_ context.Context, id int64, _ string) (User, error) {
	if f.user.ID != id {
		return User{}, ErrNotFound
	}
	return f.user, nil
}

func (f *fakeUsers) DeleteUser(context.Context, int64) error {
	if f.delErr != nil {
		return f.delErr
	}
	f.deleted = true
	return nil
}

type recordingRemover struct {
	root    string
	err     error
	address string
}

func (r *recordingRemover) RemoveInbox(_ context.Context, address string) error {
	r.address = address
	if r.err != nil {
		return r.err
	}
	if r.root == "" {
		return nil
	}
	local, domain, _ := strings.Cut(address, "@")
	return os.RemoveAll(filepath.Join(r.root, domain, local))
}

func TestDeleteUserRemovesInboxBeforeTheRow(t *testing.T) {
	root := t.TempDir()
	mail := Maildir{Root: root, UID: os.Getuid(), GID: os.Getgid()}
	if err := mail.CreateInbox("ada.probe@example.com"); err != nil {
		t.Fatal(err)
	}
	users := &fakeUsers{user: User{ID: 7, Address: "ada.probe@example.com"}}
	remover := &recordingRemover{root: root}
	server := &Server{users: users, mail: mail, inboxes: remover}

	if _, err := server.DeleteUser(context.Background(), &directoryv1.DeleteUserRequest{Id: 7}); err != nil {
		t.Fatal(err)
	}
	if remover.address != "ada.probe@example.com" || !users.deleted {
		t.Fatalf("address %q deleted %v", remover.address, users.deleted)
	}
	if _, err := os.Lstat(filepath.Join(root, "example.com", "ada.probe")); !os.IsNotExist(err) {
		t.Fatalf("home still present: %v", err)
	}
}

func TestDeleteUserKeepsTheRowWhenRemovalFails(t *testing.T) {
	users := &fakeUsers{user: User{ID: 7, Address: "ada.probe@example.com"}}
	server := &Server{
		users:   users,
		inboxes: &recordingRemover{err: errors.New("busy")},
	}
	_, err := server.DeleteUser(context.Background(), &directoryv1.DeleteUserRequest{Id: 7})
	if status.Code(err) != codes.Internal {
		t.Fatalf("code %v", status.Code(err))
	}
	if users.deleted {
		t.Fatal("deleted the row after a mailbox failure")
	}
}

func TestDeleteUserRestoresAnEmptyInboxWhenTheRowRemains(t *testing.T) {
	root := t.TempDir()
	mail := Maildir{Root: root, UID: os.Getuid(), GID: os.Getgid()}
	if err := mail.CreateInbox("ada.probe@example.com"); err != nil {
		t.Fatal(err)
	}
	users := &fakeUsers{user: User{ID: 7, Address: "ada.probe@example.com"}, delErr: errors.New("db")}
	server := &Server{users: users, mail: mail, inboxes: &recordingRemover{root: root}}
	_, err := server.DeleteUser(context.Background(), &directoryv1.DeleteUserRequest{Id: 7})
	if status.Code(err) != codes.Internal {
		t.Fatalf("code %v", status.Code(err))
	}
	if _, err := os.Stat(filepath.Join(root, "example.com", "ada.probe", "Maildir", "new")); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteUserUnknown(t *testing.T) {
	remover := &recordingRemover{}
	server := &Server{users: &fakeUsers{}, inboxes: remover}
	_, err := server.DeleteUser(context.Background(), &directoryv1.DeleteUserRequest{Id: 4})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("code %v", status.Code(err))
	}
	if remover.address != "" {
		t.Fatalf("remover called for %s", remover.address)
	}
}
