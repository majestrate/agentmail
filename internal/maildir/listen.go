package maildir

import (
	"fmt"
	"net"
	"strconv"
)

// ListenAddr requires a loopback host and a high port. The service deletes
// mailbox files, so it stays reachable only from this pod's network namespace.
func ListenAddr(raw string) (string, error) {
	if raw == "" {
		raw = "127.0.0.1:50056"
	}
	host, port, err := net.SplitHostPort(raw)
	if err != nil {
		return "", fmt.Errorf("MAILDIR_LISTEN: %w", err)
	}
	if host != "127.0.0.1" {
		return "", fmt.Errorf("MAILDIR_LISTEN must use 127.0.0.1")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1024 || n > 65535 {
		return "", fmt.Errorf("MAILDIR_LISTEN port must be between 1024 and 65535")
	}
	return net.JoinHostPort(host, strconv.Itoa(n)), nil
}
