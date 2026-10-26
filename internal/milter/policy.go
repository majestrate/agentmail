package milter

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// RejectError is a permanent SMTP refusal. Any other error is temporary.
type RejectError struct {
	Reason string
}

func (e *RejectError) Error() string {
	return e.Reason
}

func reject(reason string) error {
	return &RejectError{Reason: reason}
}

var domainPattern = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)

// ParseDomains splits MAIL_DOMAIN the same way Postfix does: commas or
// whitespace, lowercased, no duplicates.
func ParseDomains(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("MAIL_DOMAIN is required")
	}
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	})
	if len(fields) == 0 {
		return nil, fmt.Errorf("MAIL_DOMAIN is required")
	}
	seen := make(map[string]struct{}, len(fields))
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if !domainPattern.MatchString(field) {
			return nil, fmt.Errorf("invalid MAIL_DOMAIN %s", field)
		}
		domain := strings.ToLower(field)
		if _, ok := seen[domain]; ok {
			continue
		}
		seen[domain] = struct{}{}
		out = append(out, domain)
	}
	return out, nil
}

// Lookup reports whether a canonical address is a user or a distribution list.
// A database error is temporary. A missing row is (false, nil).
type Lookup interface {
	RecipientExists(ctx context.Context, address string) (bool, error)
}

// Policy is the inbound SMTP filter. The null sender and any bare address
// may send. A recipient must belong to a configured domain and exist as a
// user or a distribution list.
type Policy struct {
	domains []string
	lookup  Lookup
}

// NewPolicy returns a policy for domains. domains must already be canonical.
func NewPolicy(domains []string, lookup Lookup) (*Policy, error) {
	if len(domains) == 0 {
		return nil, fmt.Errorf("MAIL_DOMAIN is required")
	}
	if lookup == nil {
		return nil, fmt.Errorf("milter lookup is required")
	}
	copied := make([]string, len(domains))
	copy(copied, domains)
	return &Policy{domains: copied, lookup: lookup}, nil
}

// Sender accepts the null sender and any address the protocol already
// canonicalized. External senders are how a smart host delivers inbound mail.
func (p *Policy) Sender(_ context.Context, _ string) error {
	return nil
}

// Recipient accepts a canonical address in a local domain that exists.
func (p *Policy) Recipient(ctx context.Context, address string) error {
	if !p.allows(address) {
		return reject("recipient domain rejected")
	}
	found, err := p.lookup.RecipientExists(ctx, address)
	if err != nil {
		return err
	}
	if !found {
		return reject("unknown recipient")
	}
	return nil
}

func (p *Policy) allows(address string) bool {
	at := strings.LastIndex(address, "@")
	if at <= 0 || at == len(address)-1 {
		return false
	}
	domain := address[at+1:]
	for _, candidate := range p.domains {
		if domain == candidate {
			return true
		}
	}
	return false
}
