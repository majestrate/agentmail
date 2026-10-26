package directory

import (
	"context"
	"time"

	maildirv1 "agentmail/gen/agentmail/maildir/v1"
)

// InboxRemover deletes the mailbox directory for one canonical address.
type InboxRemover interface {
	RemoveInbox(ctx context.Context, address string) error
}

// MaildirClient calls the maildir service in this pod.
type MaildirClient struct {
	API maildirv1.MaildirClient
}

func (c MaildirClient) RemoveInbox(ctx context.Context, address string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	_, err := c.API.RemoveInbox(ctx, &maildirv1.RemoveInboxRequest{Address: address})
	return err
}
