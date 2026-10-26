package submissionauth

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

type fakeAuth struct {
	user string
	pass string
	err  error
}

func (f fakeAuth) Authenticate(_ context.Context, username, password string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if username != f.user || password != f.pass {
		return "", ErrDenied
	}
	return f.user, nil
}

func startServer(t *testing.T, auth Authenticator) (net.Conn, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = Serve(ctx, ln, auth)
	}()
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	return conn, func() {
		_ = conn.Close()
		cancel()
		<-done
	}
}

func handshake(t *testing.T, conn net.Conn) {
	t.Helper()
	if _, err := conn.Write([]byte("VERSION\t1\t0\nCPID\t1\n")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 512)
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	text := string(buf[:n])
	mech := strings.Index(text, "MECH\tPLAIN\tplaintext\n")
	spid := strings.Index(text, "SPID\t")
	done := strings.Index(text, "DONE\n")
	if !strings.HasPrefix(text, "VERSION\t1\t") || mech < 0 || spid < mech || done < spid {
		t.Fatalf("handshake %q", text)
	}
}

func readReply(t *testing.T, conn net.Conn) string {
	t.Helper()
	buf := make([]byte, 512)
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	return string(buf[:n])
}

func TestPlainAcceptsPassword(t *testing.T) {
	conn, stop := startServer(t, fakeAuth{user: "a@mail.internal", pass: "secret"})
	defer stop()
	handshake(t, conn)
	resp := base64.StdEncoding.EncodeToString([]byte("\x00a@mail.internal\x00secret"))
	if _, err := conn.Write([]byte("AUTH\t7\tPLAIN\tservice=smtp\tresp=" + resp + "\n")); err != nil {
		t.Fatal(err)
	}
	if got := readReply(t, conn); got != "OK\t7\tuser=a@mail.internal\n" {
		t.Fatalf("reply %q", got)
	}
}

func TestPlainRejectsPassword(t *testing.T) {
	conn, stop := startServer(t, fakeAuth{user: "a@mail.internal", pass: "secret"})
	defer stop()
	handshake(t, conn)
	resp := base64.StdEncoding.EncodeToString([]byte("\x00a@mail.internal\x00nope"))
	if _, err := conn.Write([]byte("AUTH\t3\tPLAIN\tresp=" + resp + "\n")); err != nil {
		t.Fatal(err)
	}
	if got := readReply(t, conn); got != "FAIL\t3\n" {
		t.Fatalf("reply %q", got)
	}
}

func TestLoginExchange(t *testing.T) {
	conn, stop := startServer(t, fakeAuth{user: "a@mail.internal", pass: "secret"})
	defer stop()
	handshake(t, conn)
	if _, err := conn.Write([]byte("AUTH\t4\tLOGIN\tservice=smtp\n")); err != nil {
		t.Fatal(err)
	}
	if got := readReply(t, conn); got != "CONT\t4\t"+base64.StdEncoding.EncodeToString([]byte("Username:"))+"\n" {
		t.Fatalf("username challenge %q", got)
	}
	user := base64.StdEncoding.EncodeToString([]byte("a@mail.internal"))
	if _, err := conn.Write([]byte("CONT\t4\t" + user + "\n")); err != nil {
		t.Fatal(err)
	}
	if got := readReply(t, conn); got != "CONT\t4\t"+base64.StdEncoding.EncodeToString([]byte("Password:"))+"\n" {
		t.Fatalf("password challenge %q", got)
	}
	pass := base64.StdEncoding.EncodeToString([]byte("secret"))
	if _, err := conn.Write([]byte("CONT\t4\t" + pass + "\n")); err != nil {
		t.Fatal(err)
	}
	if got := readReply(t, conn); got != "OK\t4\tuser=a@mail.internal\n" {
		t.Fatalf("reply %q", got)
	}
}

func TestBackendErrorDropsConnection(t *testing.T) {
	conn, stop := startServer(t, fakeAuth{err: errors.New("database")})
	defer stop()
	handshake(t, conn)
	resp := base64.StdEncoding.EncodeToString([]byte("\x00a@mail.internal\x00secret"))
	if _, err := conn.Write([]byte("AUTH\t1\tPLAIN\tresp=" + resp + "\n")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := conn.Read(buf)
	if n != 0 || err == nil {
		t.Fatalf("backend failure stayed open: %q %v", buf[:n], err)
	}
}

func TestPasswordQueryEmbedded(t *testing.T) {
	body, err := sqlFiles.ReadFile("sql/user_password.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.TrimSpace(string(body))
	if text != "SELECT password_hash FROM users WHERE address = $1" {
		t.Fatalf("query %q", text)
	}
}
