package maildir

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	maildirv1 "agentmail/gen/agentmail/maildir/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func TestRemoveInboxRPC(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "example.com", "ada.probe", "Maildir", "cur")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	grpcServer := grpc.NewServer()
	maildirv1.RegisterMaildirServer(grpcServer, &Server{Root: root})
	go grpcServer.Serve(lis)
	t.Cleanup(grpcServer.Stop)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	client := maildirv1.NewMaildirClient(conn)

	if _, err := client.RemoveInbox(context.Background(), &maildirv1.RemoveInboxRequest{Address: "Ada.Probe@Example.COM"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, "example.com")); !os.IsNotExist(err) {
		t.Fatalf("domain still present: %v", err)
	}
	_, err = client.RemoveInbox(context.Background(), &maildirv1.RemoveInboxRequest{Address: "Ada <ada.probe@example.com>"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code %v", status.Code(err))
	}
}
