package milter

import (
	"context"
	"errors"
	"testing"
)

func TestParseDomains(t *testing.T) {
	got, err := ParseDomains(" Mail.Internal, mail.internal  other.test ")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "mail.internal" || got[1] != "other.test" {
		t.Fatalf("domains %v", got)
	}
	for _, raw := range []string{"", "   ", "ex_ample.com", "bad/domain"} {
		if _, err := ParseDomains(raw); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}

type recordingLookup struct {
	found map[string]bool
	calls []string
	err   error
}

func (r *recordingLookup) RecipientExists(_ context.Context, address string) (bool, error) {
	r.calls = append(r.calls, address)
	if r.err != nil {
		return false, r.err
	}
	return r.found[address], nil
}

func TestRecipientPolicy(t *testing.T) {
	lookup := &recordingLookup{found: map[string]bool{
		"ada@mail.internal":  true,
		"crew@mail.internal": true,
	}}
	policy, err := NewPolicy([]string{"mail.internal"}, lookup)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := policy.Sender(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if err := policy.Sender(ctx, "out@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := policy.Recipient(ctx, "ada@mail.internal"); err != nil {
		t.Fatal(err)
	}
	if err := policy.Recipient(ctx, "crew@mail.internal"); err != nil {
		t.Fatal(err)
	}
	err = policy.Recipient(ctx, "ada@example.com")
	var rejected *RejectError
	if !errors.As(err, &rejected) || rejected.Reason != "recipient domain rejected" {
		t.Fatalf("foreign recipient: %v", err)
	}
	if len(lookup.calls) != 2 {
		t.Fatalf("foreign recipient consulted the database: %v", lookup.calls)
	}
	err = policy.Recipient(ctx, "missing@mail.internal")
	if !errors.As(err, &rejected) || rejected.Reason != "unknown recipient" {
		t.Fatalf("unknown recipient: %v", err)
	}
	lookup.err = errors.New("db down")
	err = policy.Recipient(ctx, "ada@mail.internal")
	if err == nil || errors.As(err, &rejected) {
		t.Fatalf("database error: %v", err)
	}
}
