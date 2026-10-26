package submissionauth

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	maxLine       = 8192
	authTimeout   = 10 * time.Second
	protocolMajor = 1
)

// Authenticator checks a mailbox password. ErrDenied is a permanent refusal.
// Any other error is treated as a temporary failure and drops the connection.
type Authenticator interface {
	Authenticate(ctx context.Context, username, password string) (string, error)
}

// Serve accepts Dovecot SASL clients. Postfix dials this with
// smtpd_sasl_type=dovecot and smtpd_sasl_path=inet:host:port.
func Serve(ctx context.Context, ln net.Listener, auth Authenticator) error {
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go serveConn(ctx, conn, auth)
	}
}

func serveConn(ctx context.Context, conn net.Conn, auth Authenticator) {
	defer conn.Close()
	reader := bufio.NewReaderSize(conn, maxLine)
	writer := bufio.NewWriter(conn)
	session := &session{auth: auth, w: writer}
	handshook := false
	for {
		line, err := readLine(reader)
		if err != nil {
			if !errors.Is(err, io.EOF) && ctx.Err() == nil {
				log.Printf("submission auth read: %v", err)
			}
			return
		}
		fields := strings.Split(line, "\t")
		if len(fields) == 0 || fields[0] == "" {
			continue
		}
		switch fields[0] {
		case "VERSION":
			if len(fields) < 3 {
				log.Printf("submission auth version line is short")
				return
			}
			major, err := strconv.Atoi(fields[1])
			if err != nil || major != protocolMajor {
				log.Printf("submission auth protocol major %q", fields[1])
				return
			}
			if !handshook {
				if err := session.handshake(); err != nil {
					log.Printf("submission auth handshake: %v", err)
					return
				}
				handshook = true
			}
		case "CPID":
			continue
		case "AUTH":
			if !handshook {
				return
			}
			if err := session.authRequest(ctx, fields[1:]); err != nil {
				log.Printf("submission auth: %v", err)
				return
			}
		case "CONT":
			if !handshook {
				return
			}
			if err := session.continueRequest(ctx, fields[1:]); err != nil {
				log.Printf("submission auth: %v", err)
				return
			}
		default:
			continue
		}
	}
}

type session struct {
	auth    Authenticator
	w       *bufio.Writer
	pending *pendingAuth
}

type pendingAuth struct {
	id       string
	mech     string
	username string
	haveUser bool
}

func (s *session) handshake() error {
	pid := os.Getpid()
	_, err := fmt.Fprintf(s.w, "VERSION\t1\t0\nMECH\tPLAIN\tplaintext\nMECH\tLOGIN\tplaintext\nSPID\t%d\nCUID\t%d\nDONE\n", pid, pid)
	if err != nil {
		return err
	}
	return s.w.Flush()
}

func (s *session) authRequest(ctx context.Context, fields []string) error {
	if len(fields) < 2 || !numericID(fields[0]) {
		return fmt.Errorf("auth request")
	}
	id := fields[0]
	mech := strings.ToUpper(fields[1])
	params := parameters(fields[2:])
	s.pending = nil
	switch mech {
	case "PLAIN":
		resp, ok := params["resp"]
		if !ok {
			s.pending = &pendingAuth{id: id, mech: mech}
			return s.cont(id, "")
		}
		return s.finishPlain(ctx, id, resp)
	case "LOGIN":
		pending := &pendingAuth{id: id, mech: mech}
		if resp, ok := params["resp"]; ok {
			user, err := decodeText(resp)
			if err != nil {
				return s.fail(id)
			}
			pending.username = user
			pending.haveUser = true
			s.pending = pending
			return s.cont(id, "Password:")
		}
		s.pending = pending
		return s.cont(id, "Username:")
	default:
		return s.fail(id)
	}
}

func (s *session) continueRequest(ctx context.Context, fields []string) error {
	if len(fields) < 2 || !numericID(fields[0]) || s.pending == nil || fields[0] != s.pending.id {
		if len(fields) > 0 && numericID(fields[0]) {
			return s.fail(fields[0])
		}
		return fmt.Errorf("continuation")
	}
	id := fields[0]
	payload := fields[1]
	switch s.pending.mech {
	case "PLAIN":
		s.pending = nil
		return s.finishPlain(ctx, id, payload)
	case "LOGIN":
		text, err := decodeText(payload)
		if err != nil {
			s.pending = nil
			return s.fail(id)
		}
		if !s.pending.haveUser {
			s.pending.username = text
			s.pending.haveUser = true
			return s.cont(id, "Password:")
		}
		username := s.pending.username
		s.pending = nil
		return s.finish(ctx, id, username, text)
	default:
		s.pending = nil
		return s.fail(id)
	}
}

func (s *session) finishPlain(ctx context.Context, id, resp string) error {
	raw, err := decodeBytes(resp)
	if err != nil {
		return s.fail(id)
	}
	parts := strings.Split(string(raw), "\x00")
	if len(parts) != 3 {
		return s.fail(id)
	}
	authzid, username, password := parts[0], parts[1], parts[2]
	if authzid != "" && authzid != username {
		return s.fail(id)
	}
	return s.finish(ctx, id, username, password)
}

func (s *session) finish(ctx context.Context, id, username, password string) error {
	actx, cancel := context.WithTimeout(ctx, authTimeout)
	defer cancel()
	user, err := s.auth.Authenticate(actx, username, password)
	if err != nil {
		if errors.Is(err, ErrDenied) {
			return s.fail(id)
		}
		return err
	}
	if strings.ContainsAny(user, "\t\r\n") || user == "" {
		return s.fail(id)
	}
	_, err = fmt.Fprintf(s.w, "OK\t%s\tuser=%s\n", id, user)
	if err != nil {
		return err
	}
	return s.w.Flush()
}

func (s *session) cont(id, challenge string) error {
	_, err := fmt.Fprintf(s.w, "CONT\t%s\t%s\n", id, base64.StdEncoding.EncodeToString([]byte(challenge)))
	if err != nil {
		return err
	}
	return s.w.Flush()
}

func (s *session) fail(id string) error {
	_, err := fmt.Fprintf(s.w, "FAIL\t%s\n", id)
	if err != nil {
		return err
	}
	return s.w.Flush()
}

func parameters(fields []string) map[string]string {
	out := make(map[string]string, len(fields))
	for _, field := range fields {
		key, value, ok := strings.Cut(field, "=")
		if ok && key != "" {
			out[key] = value
		}
	}
	return out
}

func numericID(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func decodeText(value string) (string, error) {
	raw, err := decodeBytes(value)
	if err != nil {
		return "", err
	}
	if strings.ContainsAny(string(raw), "\r\n") {
		return "", fmt.Errorf("credential contains a newline")
	}
	return string(raw), nil
}

func decodeBytes(value string) ([]byte, error) {
	if strings.ContainsAny(value, "\t\r\n") {
		return nil, fmt.Errorf("encoding")
	}
	raw, err := base64.StdEncoding.DecodeString(value)
	if err == nil {
		return raw, nil
	}
	return base64.RawStdEncoding.DecodeString(value)
}

func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if len(line) > maxLine {
		return "", fmt.Errorf("line too long")
	}
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
