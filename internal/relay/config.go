package relay

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
)

// domainPattern matches the Postfix virtual domain tokens the milter accepts.
var domainPattern = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)

// ParseDomains splits MAIL_DOMAIN on commas or whitespace, lowercases each
// token, and drops duplicates.
func ParseDomains(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("MAIL_DOMAIN is required")
	}
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	})
	return normalizeDomains(fields)
}

func normalizeDomains(fields []string) ([]string, error) {
	if len(fields) == 0 {
		return nil, fmt.Errorf("relay domain is required")
	}
	seen := make(map[string]struct{}, len(fields))
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if !domainPattern.MatchString(field) {
			return nil, fmt.Errorf("invalid relay domain %s", field)
		}
		domain := strings.ToLower(field)
		if _, ok := seen[domain]; ok {
			continue
		}
		seen[domain] = struct{}{}
		out = append(out, domain)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("relay domain is required")
	}
	return out, nil
}

// ParseAllow parses a comma- or whitespace-separated CIDR list. An empty
// list is rejected. A prefix length of zero is rejected.
func ParseAllow(raw string) ([]*net.IPNet, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("RELAY_VPN_ALLOW is required")
	}
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	})
	if len(fields) == 0 {
		return nil, fmt.Errorf("RELAY_VPN_ALLOW is required")
	}
	out := make([]*net.IPNet, 0, len(fields))
	for _, field := range fields {
		_, network, err := net.ParseCIDR(field)
		if err != nil {
			return nil, fmt.Errorf("invalid relay allow network %s", field)
		}
		ones, _ := network.Mask.Size()
		if ones == 0 {
			return nil, fmt.Errorf("refusing world-open network %s", field)
		}
		out = append(out, network)
	}
	return out, nil
}

// ParseUpstream requires host:port. An IP host is required when ipHost is set,
// so a name that CoreDNS rewrites to the internal Postfix cannot be used as
// the public MX hop.
func ParseUpstream(name, raw string, ipHost bool) (string, error) {
	host, port, err := net.SplitHostPort(raw)
	if err != nil || host == "" || port == "" {
		return "", fmt.Errorf("%s must be host:port", name)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("%s port must be between 1 and 65535", name)
	}
	if ipHost && net.ParseIP(host) == nil {
		return "", fmt.Errorf("%s host must be an IP address", name)
	}
	return raw, nil
}
