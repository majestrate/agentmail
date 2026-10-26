//go:generate protoc -I ../../proto -I /usr/include --go_out=../.. --go_opt=module=agentmail --go-grpc_out=../.. --go-grpc_opt=module=agentmail ../../proto/agentmail/maildir/v1/maildir.proto

// Command maildir deletes mailbox directories. It listens on loopback and
// speaks plaintext gRPC. The directory container in the same pod is the
// caller. The process runs as the mail user so it can remove mode-0700 homes.
package main

import (
	"context"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	maildirv1 "agentmail/gen/agentmail/maildir/v1"
	"agentmail/internal/maildir"

	"google.golang.org/grpc"
)

func main() {
	log.SetFlags(0)
	if os.Getuid() == 0 {
		log.Fatal("refusing to run the maildir service as root")
	}
	listenAddr, err := maildir.ListenAddr(os.Getenv("MAILDIR_LISTEN"))
	if err != nil {
		log.Fatal(err)
	}
	root := os.Getenv("MAIL_HOME_ROOT")
	if root == "" {
		root = "/var/mail/vhosts"
	}
	lis, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatal(err)
	}
	server := grpc.NewServer()
	maildirv1.RegisterMaildirServer(server, &maildir.Server{Root: root})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		server.GracefulStop()
	}()
	log.Printf("maildir listening on %s", listenAddr)
	if err := server.Serve(lis); err != nil {
		log.Fatal(err)
	}
}
