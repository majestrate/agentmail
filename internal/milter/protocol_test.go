package milter

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func startServer(t *testing.T, decider Decider) (net.Conn, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = Serve(ctx, ln, decider)
	}()
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	return conn, func() {
		_ = conn.Close()
		cancel()
		<-done
	}
}

func writeCmd(t *testing.T, conn net.Conn, cmd byte, data []byte) {
	t.Helper()
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(1+len(data)))
	if _, err := conn.Write(hdr[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte{cmd}); err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		return
	}
	if _, err := conn.Write(data); err != nil {
		t.Fatal(err)
	}
}

func readCmd(t *testing.T, conn net.Conn) (byte, []byte) {
	t.Helper()
	var n uint32
	if err := binary.Read(conn, binary.BigEndian, &n); err != nil {
		t.Fatal(err)
	}
	if n == 0 || n > maxFrame {
		t.Fatalf("frame length %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	return buf[0], buf[1:]
}

func optneg(version, actions, protocol uint32) []byte {
	buf := make([]byte, 12)
	binary.BigEndian.PutUint32(buf[0:4], version)
	binary.BigEndian.PutUint32(buf[4:8], actions)
	binary.BigEndian.PutUint32(buf[8:12], protocol)
	return buf
}

func handshake(t *testing.T, conn net.Conn) {
	t.Helper()
	writeCmd(t, conn, cmdOptNeg, optneg(8, 0xffffffff, 0xffffffff))
	cmd, data := readCmd(t, conn)
	if cmd != cmdOptNeg || len(data) != 12 {
		t.Fatalf("optneg %q %d", cmd, len(data))
	}
	if got := binary.BigEndian.Uint32(data[0:4]); got != protocolVersion {
		t.Fatalf("version %d", got)
	}
	if binary.BigEndian.Uint32(data[4:8]) != 0 || binary.BigEndian.Uint32(data[8:12]) != 0 {
		t.Fatalf("negotiation offered modifications or skipped stages")
	}
}

func cstr(parts ...string) []byte {
	var buf bytes.Buffer
	for _, part := range parts {
		buf.WriteString(part)
		buf.WriteByte(0)
	}
	return buf.Bytes()
}

func expectContinue(t *testing.T, conn net.Conn) {
	t.Helper()
	cmd, data := readCmd(t, conn)
	if cmd != repContinue || len(data) != 0 {
		t.Fatalf("continue got %q %q", cmd, data)
	}
}

func expectReply(t *testing.T, conn net.Conn, want byte, text string) {
	t.Helper()
	cmd, data := readCmd(t, conn)
	if cmd != want {
		t.Fatalf("reply %q data %q, want %q", cmd, data, want)
	}
	if want == repReply {
		got := strings.TrimRight(string(data), "\x00")
		if got != text {
			t.Fatalf("smtp reply %q, want %q", got, text)
		}
		if !bytes.HasSuffix(data, []byte{0}) {
			t.Fatal("reply code is not NUL terminated")
		}
	}
}

func TestNegotiationAndInboundMessage(t *testing.T) {
	lookup := &recordingLookup{found: map[string]bool{
		"ada@mail.internal":  true,
		"crew@mail.internal": true,
	}}
	policy, err := NewPolicy([]string{"mail.internal"}, lookup)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	conn, stop := startServer(t, policy)
	defer stop()
	handshake(t, conn)

	writeCmd(t, conn, cmdMacro, cstr("j", "mail.internal"))
	_ = conn.SetReadDeadline(time.Now().Add(80 * time.Millisecond))
	if _, err := readCmdMaybe(conn); err == nil {
		t.Fatal("macro produced a reply")
	}
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))

	writeCmd(t, conn, cmdConnect, cstr("smart.test"))
	expectContinue(t, conn)
	writeCmd(t, conn, cmdHelo, cstr("smart.test"))
	expectContinue(t, conn)

	writeCmd(t, conn, cmdMail, cstr("<out@example.com>", "SIZE=12"))
	expectContinue(t, conn)
	writeCmd(t, conn, cmdRcpt, cstr("<ada@mail.internal>"))
	expectContinue(t, conn)
	writeCmd(t, conn, cmdRcpt, cstr("<crew@mail.internal>"))
	expectContinue(t, conn)
	writeCmd(t, conn, cmdRcpt, cstr("<missing@mail.internal>"))
	expectReply(t, conn, repReply, "550 5.7.1 unknown recipient")
	writeCmd(t, conn, cmdRcpt, cstr("<ada@example.com>"))
	expectReply(t, conn, repReply, "550 5.7.1 recipient domain rejected")

	writeCmd(t, conn, cmdHeader, cstr("Subject", " hello"))
	expectContinue(t, conn)
	writeCmd(t, conn, cmdEOH, nil)
	expectContinue(t, conn)
	writeCmd(t, conn, cmdData, nil)
	expectContinue(t, conn)
	writeCmd(t, conn, cmdBody, []byte("hello"))
	expectContinue(t, conn)
	writeCmd(t, conn, cmdBodyEOB, []byte("!"))
	cmd, data := readCmd(t, conn)
	if cmd != repAccept || len(data) != 0 {
		t.Fatalf("accept got %q %q", cmd, data)
	}
	if !strings.Contains(logs.String(), "milter accept from=out@example.com to=ada@mail.internal,crew@mail.internal bytes=6") {
		t.Fatalf("accept log %q", logs.String())
	}
}

func readCmdMaybe(conn net.Conn) (byte, error) {
	var n uint32
	if err := binary.Read(conn, binary.BigEndian, &n); err != nil {
		return 0, err
	}
	return 0, nil
}

func TestSenderShapes(t *testing.T) {
	lookup := &recordingLookup{found: map[string]bool{"ada@mail.internal": true}}
	policy, err := NewPolicy([]string{"mail.internal"}, lookup)
	if err != nil {
		t.Fatal(err)
	}
	conn, stop := startServer(t, policy)
	defer stop()
	handshake(t, conn)

	writeCmd(t, conn, cmdMail, cstr("Ada <ada@mail.internal>"))
	expectReply(t, conn, repReply, "550 5.7.1 malformed address")
	writeCmd(t, conn, cmdMail, cstr("<Ada Probe <ada@mail.internal>>"))
	expectReply(t, conn, repReply, "550 5.7.1 malformed address")
	writeCmd(t, conn, cmdMail, cstr("<>"))
	expectContinue(t, conn)
	writeCmd(t, conn, cmdRcpt, cstr("<>"))
	expectReply(t, conn, repReply, "550 5.7.1 malformed address")
	writeCmd(t, conn, cmdRcpt, cstr("<missing@mail.internal>"))
	expectReply(t, conn, repReply, "550 5.7.1 unknown recipient")
	writeCmd(t, conn, cmdBodyEOB, nil)
	expectReply(t, conn, repReply, "550 5.7.1 no valid recipients")

	writeCmd(t, conn, cmdMail, cstr("<>"))
	expectContinue(t, conn)
	writeCmd(t, conn, cmdRcpt, cstr("<Ada@Mail.Internal>"))
	expectContinue(t, conn)
	if len(lookup.calls) == 0 || lookup.calls[len(lookup.calls)-1] != "ada@mail.internal" {
		t.Fatalf("lookup %v", lookup.calls)
	}
	writeCmd(t, conn, cmdBodyEOB, nil)
	cmd, _ := readCmd(t, conn)
	if cmd != repAccept {
		t.Fatalf("null sender %q", cmd)
	}
}

func TestDatabaseErrorTempfails(t *testing.T) {
	lookup := &recordingLookup{err: errors.New("db down")}
	policy, err := NewPolicy([]string{"mail.internal"}, lookup)
	if err != nil {
		t.Fatal(err)
	}
	conn, stop := startServer(t, policy)
	defer stop()
	handshake(t, conn)
	writeCmd(t, conn, cmdMail, cstr("<out@example.com>"))
	expectContinue(t, conn)
	writeCmd(t, conn, cmdRcpt, cstr("<ada@mail.internal>"))
	expectReply(t, conn, repTempfail, "")
	writeCmd(t, conn, cmdBodyEOB, nil)
	expectReply(t, conn, repTempfail, "")
}

func TestAbortResetsMessage(t *testing.T) {
	lookup := &recordingLookup{found: map[string]bool{"ada@mail.internal": true}}
	policy, err := NewPolicy([]string{"mail.internal"}, lookup)
	if err != nil {
		t.Fatal(err)
	}
	conn, stop := startServer(t, policy)
	defer stop()
	handshake(t, conn)
	writeCmd(t, conn, cmdMail, cstr("<out@example.com>"))
	expectContinue(t, conn)
	writeCmd(t, conn, cmdRcpt, cstr("<ada@mail.internal>"))
	expectContinue(t, conn)
	writeCmd(t, conn, cmdAbort, nil)
	_ = conn.SetReadDeadline(time.Now().Add(80 * time.Millisecond))
	if _, err := readCmdMaybe(conn); err == nil {
		t.Fatal("abort produced a reply")
	}
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	writeCmd(t, conn, cmdBodyEOB, nil)
	expectReply(t, conn, repReply, "550 5.7.1 no valid recipients")

	writeCmd(t, conn, cmdMail, cstr("<out@example.com>"))
	expectContinue(t, conn)
	writeCmd(t, conn, cmdRcpt, cstr("ada@mail.internal"))
	expectContinue(t, conn)
	writeCmd(t, conn, cmdBodyEOB, nil)
	cmd, _ := readCmd(t, conn)
	if cmd != repAccept {
		t.Fatalf("second message %q", cmd)
	}
}

func TestQuitCloses(t *testing.T) {
	policy, err := NewPolicy([]string{"mail.internal"}, &recordingLookup{})
	if err != nil {
		t.Fatal(err)
	}
	conn, stop := startServer(t, policy)
	defer stop()
	handshake(t, conn)
	writeCmd(t, conn, cmdQuit, nil)
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	var n uint32
	err = binary.Read(conn, binary.BigEndian, &n)
	if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("quit read %v", err)
	}
}

func TestQueriesEmbedded(t *testing.T) {
	for _, name := range []string{"sql/user_address.sql", "sql/list_address.sql"} {
		body, err := sqlFiles.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(body)) == "" {
			t.Fatalf("%s is empty", name)
		}
	}
}
