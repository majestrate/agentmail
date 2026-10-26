package maildir

import (
	"context"
	"log"

	maildirv1 "agentmail/gen/agentmail/maildir/v1"
	"agentmail/internal/directory"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Server removes mailbox directories. It does not open the user database.
type Server struct {
	maildirv1.UnimplementedMaildirServer
	Root string
}

func (s *Server) RemoveInbox(ctx context.Context, req *maildirv1.RemoveInboxRequest) (*maildirv1.RemoveInboxResponse, error) {
	address, err := directory.CanonicalAddress(req.GetAddress())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := RemoveInbox(s.Root, address); err != nil {
		log.Printf("maildir: remove %s: %v", address, err)
		return nil, status.Error(codes.Internal, "internal error")
	}
	log.Printf("maildir removed %s", address)
	return &maildirv1.RemoveInboxResponse{}, nil
}
