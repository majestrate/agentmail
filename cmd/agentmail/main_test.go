package main

import (
	"bytes"
	"context"
	"net"
	"strings"
	"testing"

	directoryv1 "agentmail/gen/agentmail/directory/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeDirectory struct {
	directoryv1.UnimplementedDirectoryServer
	user *directoryv1.User
}

func (f *fakeDirectory) CreateUser(_ context.Context, req *directoryv1.CreateUserRequest) (*directoryv1.User, error) {
	if req.GetPassword() == "" {
		return nil, status.Error(codes.InvalidArgument, "password is required")
	}
	f.user = &directoryv1.User{Id: 4, Address: req.GetAddress(), DisplayName: req.GetDisplayName()}
	return f.user, nil
}

func (f *fakeDirectory) ListUsers(context.Context, *directoryv1.ListUsersRequest) (*directoryv1.ListUsersResponse, error) {
	if f.user == nil {
		return &directoryv1.ListUsersResponse{}, nil
	}
	return &directoryv1.ListUsersResponse{Users: []*directoryv1.User{f.user}}, nil
}

func TestUsersCreateAndList(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	fake := &fakeDirectory{}
	directoryv1.RegisterDirectoryServer(server, fake)
	go server.Serve(lis)
	t.Cleanup(server.Stop)

	var out bytes.Buffer
	err = run([]string{"-server", lis.Addr().String(), "users", "create", "-address", "a@example.com", "-display-name", "A", "-password", "secret"}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "id=4 address=a@example.com display_name=A") {
		t.Fatalf("create output: %s", out.String())
	}
	out.Reset()
	err = run([]string{"-server", lis.Addr().String(), "users", "ls"}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "address=a@example.com") {
		t.Fatalf("list output: %s", out.String())
	}
}

func TestUsersCreateRequiresPassword(t *testing.T) {
	err := run([]string{"users", "create", "-address", "a@example.com"}, ioDiscard())
	if err == nil || !strings.Contains(err.Error(), "-password") {
		t.Fatalf("got %v", err)
	}
}

func TestMembershipsRequiresUser(t *testing.T) {
	err := run([]string{"memberships", "ls"}, ioDiscard())
	if err == nil || !strings.Contains(err.Error(), "-user") {
		t.Fatalf("got %v", err)
	}
}

func ioDiscard() *bytes.Buffer { return &bytes.Buffer{} }
