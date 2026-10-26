package relay

import (
	"bufio"
	"context"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseDomains(t *testing.T) {
	got, err := ParseDomains("Mail.Internal, mail.internal b.example")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "mail.internal,b.example" {
		t.Fatalf("domains %v", got)
	}
	for _, raw := range []string{"", " ", "ex_ample.com", "bad/domain"} {
		if _, err := ParseDomains(raw); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}

func TestParseAllow(t *testing.T) {
	got, err := ParseAllow("203.0.113.10/32, 10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got[0].Contains(net.ParseIP("203.0.113.10")) {
		t.Fatalf("networks %v", got)
	}
	for _, raw := range []string{"", "203.0.113.10", "0.0.0.0/0", "::/0", "not-a-cidr"} {
		if _, err := ParseAllow(raw); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}

func TestParseUpstream(t *testing.T) {
	if _, err := ParseUpstream("RELAY_OUTBOUND_UPSTREAM", "203.0.113.10:25", true); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseUpstream("RELAY_OUTBOUND_UPSTREAM", "[2001:db8::10]:2525", true); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"mail.internal:25", "203.0.113.10", "203.0.113.10:0", "203.0.113.10:70000"} {
		if _, err := ParseUpstream("RELAY_OUTBOUND_UPSTREAM", raw, true); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	if _, err := ParseUpstream("RELAY_INBOUND_UPSTREAM", "postfix.example:25", false); err != nil {
		t.Fatal(err)
	}
}

func TestInboundMessage(t *testing.T) {
	h := startRelay(t, relayOpt{})
	c := h.dial(t)
	if code, _, err := c.banner(); err != nil || code != 220 {
		t.Fatalf("banner %d %v", code, err)
	}
	c.expect(t, "EHLO client.test", 250)
	c.expect(t, "MAIL FROM:<> SIZE=100 BODY=8BITMIME RET=HDRS", 250)
	c.expect(t, "RCPT TO:<Ada@Mail.Internal>", 250)
	code, text, err := c.data("Subject: hi", "", "..starts-with-dot", "plain")
	if err != nil || code != 250 {
		t.Fatalf("data %d %s %v", code, text, err)
	}
	c.expect(t, "QUIT", 221)

	txn := h.fake.wait(t)
	if txn.mail != "MAIL FROM:<> SIZE=100 BODY=8BITMIME" {
		t.Fatalf("mail %q", txn.mail)
	}
	if len(txn.rcpts) != 1 || txn.rcpts[0] != "ada@mail.internal" {
		t.Fatalf("rcpts %v", txn.rcpts)
	}
	body := strings.Join(txn.body, "\n")
	for _, want := range []string{
		"Received: from client.test ([127.0.0.1])",
		"by relay.test with ESMTP id abc123 for <ada@mail.internal>;",
		"Fri, 02 Jan 2026 03:04:05 +0000",
		"..starts-with-dot",
		"plain",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "\n.starts-with-dot") {
		t.Fatalf("leading dot was not stuffed:\n%s", body)
	}
}

func TestDirections(t *testing.T) {
	in := startRelay(t, relayOpt{})
	c := in.dial(t)
	c.expect(t, "", 220)
	c.expect(t, "EHLO client.test", 250)
	c.expect(t, "MAIL FROM:<ada@mail.internal>", 250)
	code, text, _ := c.cmd("RCPT TO:<ada@other.test>")
	if code != 550 || !strings.Contains(text, "recipient domain rejected") {
		t.Fatalf("foreign %d %s", code, text)
	}
	if txn := in.fake.snapshot(); len(txn.rcpts) != 0 {
		t.Fatalf("foreign rcpt was forwarded: %v", txn.rcpts)
	}
	c.expect(t, "RCPT TO:<ada@mail.internal>", 250)
	code, _, err := c.data("body")
	if err != nil || code != 250 {
		t.Fatalf("inbound data %d %v", code, err)
	}

	out := startRelay(t, relayOpt{outbound: true})
	c = out.dial(t)
	c.expect(t, "", 220)
	c.expect(t, "EHLO client.test", 250)
	c.expect(t, "MAIL FROM:<ada@mail.internal>", 250)
	code, text, _ = c.cmd("RCPT TO:<ada@mail.internal>")
	if code != 550 || !strings.Contains(text, "recipient domain refused") {
		t.Fatalf("local %d %s", code, text)
	}
	if txn := out.fake.snapshot(); len(txn.rcpts) != 0 {
		t.Fatalf("local rcpt was forwarded: %v", txn.rcpts)
	}
	c.expect(t, "RCPT TO:<ada@other.test>", 250)
	code, _, err = c.data("body")
	if err != nil || code != 250 {
		t.Fatalf("outbound data %d %v", code, err)
	}
	txn := out.fake.wait(t)
	if len(txn.rcpts) != 1 || txn.rcpts[0] != "ada@other.test" {
		t.Fatalf("outbound rcpts %v", txn.rcpts)
	}
}

func TestUpstreamRcptRefusal(t *testing.T) {
	h := startRelay(t, relayOpt{})
	h.fake.rcptCode = func(addr string) int {
		if addr == "nope@mail.internal" {
			return 550
		}
		return 250
	}
	c := h.dial(t)
	c.expect(t, "", 220)
	c.expect(t, "EHLO client.test", 250)
	c.expect(t, "MAIL FROM:<ada@mail.internal>", 250)
	code, text, _ := c.cmd("RCPT TO:<nope@mail.internal>")
	if code != 550 || !strings.Contains(text, "user unknown") {
		t.Fatalf("rcpt %d %s", code, text)
	}
	c.expect(t, "RCPT TO:<ada@mail.internal>", 250)
	code, _, err := c.data("ok")
	if err != nil || code != 250 {
		t.Fatalf("data %d %v", code, err)
	}
	txn := h.fake.wait(t)
	if strings.Join(txn.rcpts, ",") != "nope@mail.internal,ada@mail.internal" {
		t.Fatalf("rcpts %v", txn.rcpts)
	}
}

func TestUpstreamDataDropIsNot250(t *testing.T) {
	h := startRelay(t, relayOpt{})
	h.fake.dropData = true
	c := h.dial(t)
	c.expect(t, "", 220)
	c.expect(t, "EHLO client.test", 250)
	c.expect(t, "MAIL FROM:<ada@mail.internal>", 250)
	c.expect(t, "RCPT TO:<ada@mail.internal>", 250)
	code, text, err := c.data("hello")
	if err != nil {
		t.Fatal(err)
	}
	if code == 250 || code != 450 {
		t.Fatalf("data %d %s", code, text)
	}
	c.expect(t, "NOOP", 250)
}

func TestUpstreamDataReject(t *testing.T) {
	h := startRelay(t, relayOpt{})
	h.fake.dataCode = 550
	c := h.dial(t)
	c.expect(t, "", 220)
	c.expect(t, "EHLO client.test", 250)
	c.expect(t, "MAIL FROM:<ada@mail.internal>", 250)
	c.expect(t, "RCPT TO:<ada@mail.internal>", 250)
	code, _, err := c.data("hello")
	if err != nil || code != 550 {
		t.Fatalf("data %d %v", code, err)
	}
}

func TestAddressRules(t *testing.T) {
	h := startRelay(t, relayOpt{})
	c := h.dial(t)
	c.expect(t, "", 220)
	c.expect(t, "MAIL FROM:<ada@mail.internal>", 503)
	c.expect(t, "EHLO client.test", 250)
	code, _, _ := c.cmd("MAIL FROM:<Ada Probe <ada@mail.internal>>")
	if code != 501 {
		t.Fatalf("display name %d", code)
	}
	code, _, _ = c.cmd("MAIL FROM:<Ada Probe ada@mail.internal>")
	if code != 501 {
		t.Fatalf("bare display name %d", code)
	}
	c.expect(t, "MAIL FROM:<>", 250)
	code, _, _ = c.cmd("RCPT TO:<>")
	if code != 501 {
		t.Fatalf("null rcpt %d", code)
	}
	c.expect(t, "DATA", 503)
	c.expect(t, "RCPT TO:<ada@other.test>", 550)
	c.expect(t, "DATA", 503)
	if h.fake.snapshot().data {
		t.Fatal("DATA was forwarded without an accepted recipient")
	}
}

func TestSize(t *testing.T) {
	h := startRelay(t, relayOpt{maxSize: 1024})
	c := h.dial(t)
	c.expect(t, "", 220)
	c.expect(t, "EHLO client.test", 250)
	code, _, _ := c.cmd("MAIL FROM:<ada@mail.internal> SIZE=2048")
	if code != 552 {
		t.Fatalf("size param %d", code)
	}
	if n := h.fake.accepted.Load(); n != 0 {
		t.Fatalf("upstream dials %d", n)
	}
	c.expect(t, "MAIL FROM:<ada@mail.internal>", 250)
	c.expect(t, "RCPT TO:<ada@mail.internal>", 250)
	code, _, err := c.data(strings.Repeat("a", 2000))
	if err != nil || code != 552 {
		t.Fatalf("body %d %v", code, err)
	}
	c.expect(t, "NOOP", 250)
	if h.fake.snapshot().data {
		t.Fatal("oversized body was accepted upstream")
	}
}

func TestSessionCommands(t *testing.T) {
	h := startRelay(t, relayOpt{maxSize: 4096})
	c := h.dial(t)
	c.expect(t, "", 220)
	lines := c.ehloLines(t)
	joined := strings.Join(lines, "\n")
	for _, banned := range []string{"PIPELINING", "CHUNKING", "STARTTLS", "AUTH"} {
		if strings.Contains(strings.ToUpper(joined), banned) {
			t.Fatalf("EHLO advertised %s:\n%s", banned, joined)
		}
	}
	if !strings.Contains(joined, "250-8BITMIME") || !strings.Contains(joined, "250 SIZE 4096") {
		t.Fatalf("EHLO lines:\n%s", joined)
	}
	c.expect(t, "VRFY ada@mail.internal", 502)
	c.expect(t, "EXPN list", 502)
	c.expect(t, "STARTTLS", 502)
	c.expect(t, "AUTH PLAIN", 502)
	c.expect(t, "NOOP", 250)
	c.expect(t, "MAIL FROM:<ada@mail.internal>", 250)
	c.expect(t, "RSET", 250)
	c.expect(t, "DATA", 503)
	c.expect(t, "QUIT", 221)
}

func TestAllowlist(t *testing.T) {
	_, vpn, err := net.ParseCIDR("10.1.0.0/24")
	if err != nil {
		t.Fatal(err)
	}
	rejected := startRelay(t, relayOpt{allow: []*net.IPNet{vpn}})
	c := rejected.dial(t)
	code, text, err := c.banner()
	if err != nil || code != 550 || !strings.Contains(text, "client host rejected") {
		t.Fatalf("allow %d %s %v", code, text, err)
	}
	if _, _, err := c.banner(); err == nil {
		t.Fatal("expected the connection to stop after the allowlist refusal")
	}

	_, loop := mustCIDR(t, "127.0.0.1/32")
	allowed := startRelay(t, relayOpt{allow: []*net.IPNet{loop}})
	c = allowed.dial(t)
	c.expect(t, "", 220)
	c.expect(t, "EHLO client.test", 250)

	open := startRelay(t, relayOpt{})
	c = open.dial(t)
	c.expect(t, "", 220)
	c.expect(t, "EHLO client.test", 250)
	c.expect(t, "MAIL FROM:<ada@mail.internal>", 250)
	c.expect(t, "RCPT TO:<ada@mail.internal>", 250)
	code, _, err = c.data("via mesh")
	if err != nil || code != 250 {
		t.Fatalf("mesh %d %v", code, err)
	}
	body := strings.Join(open.fake.wait(t).body, "\n")
	if !strings.Contains(body, "[127.0.0.1]") {
		t.Fatalf("received header:\n%s", body)
	}
}

type relayOpt struct {
	outbound bool
	allow    []*net.IPNet
	maxSize  int64
}

type harness struct {
	addr string
	fake *fakeUp
}

func startRelay(t *testing.T, opt relayOpt) *harness {
	t.Helper()
	fakeLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	clientLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := newFake(fakeLn)
	go fake.serve()
	maxSize := opt.maxSize
	if maxSize == 0 {
		maxSize = 1 << 20
	}
	cfg := Config{
		Hostname:  "relay.test",
		Domains:   []string{"Mail.Internal"},
		Upstream:  fakeLn.Addr().String(),
		Allow:     opt.allow,
		Outbound:  opt.outbound,
		MaxSize:   maxSize,
		Timeout:   2 * time.Second,
		now:       func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) },
		messageID: func() string { return "abc123" },
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = Serve(ctx, clientLn, cfg)
	}()
	t.Cleanup(func() {
		cancel()
		_ = clientLn.Close()
		_ = fakeLn.Close()
	})
	return &harness{addr: clientLn.Addr().String(), fake: fake}
}

func (h *harness) dial(t *testing.T) *smtpClient {
	t.Helper()
	conn, err := net.Dial("tcp", h.addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	return &smtpClient{r: bufio.NewReader(conn), w: bufio.NewWriter(conn)}
}

type smtpClient struct {
	r *bufio.Reader
	w *bufio.Writer
}

func (c *smtpClient) banner() (int, string, error) {
	return readReply(c.r)
}

func (c *smtpClient) cmd(line string) (int, string, error) {
	if line != "" {
		if err := writeLine(c.w, line); err != nil {
			return 0, "", err
		}
	}
	return readReply(c.r)
}

func (c *smtpClient) expect(t *testing.T, line string, want int) {
	t.Helper()
	code, text, err := c.cmd(line)
	if err != nil || code != want {
		t.Fatalf("%q got %d %s %v", line, code, text, err)
	}
}

func (c *smtpClient) data(lines ...string) (int, string, error) {
	if err := writeLine(c.w, "DATA"); err != nil {
		return 0, "", err
	}
	code, text, err := readReply(c.r)
	if err != nil || code != 354 {
		return code, text, err
	}
	for _, line := range lines {
		if err := writeLine(c.w, line); err != nil {
			return 0, "", err
		}
	}
	if err := writeLine(c.w, "."); err != nil {
		return 0, "", err
	}
	return readReply(c.r)
}

func (c *smtpClient) ehloLines(t *testing.T) []string {
	t.Helper()
	if err := writeLine(c.w, "EHLO client.test"); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for {
		line, err := readLine(c.r, maxReply)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line)
		if len(line) < 4 || line[3] != '-' {
			break
		}
	}
	return lines
}

type txn struct {
	mail  string
	rcpts []string
	body  []string
	data  bool
}

type fakeUp struct {
	ln       net.Listener
	accepted atomic.Int32
	rcptCode func(addr string) int
	dataCode int
	dropData bool
	mu       sync.Mutex
	last     txn
	done     chan struct{}
}

func newFake(ln net.Listener) *fakeUp {
	return &fakeUp{ln: ln, done: make(chan struct{}, 1)}
}

func (f *fakeUp) serve() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}
		f.accepted.Add(1)
		f.handle(conn)
	}
}

func (f *fakeUp) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	if err := writeLine(w, "220 fake ESMTP"); err != nil {
		return
	}
	line, err := readLine(r, maxCommand)
	if err != nil || !strings.HasPrefix(strings.ToUpper(line), "EHLO ") {
		return
	}
	if err := writeLine(w, "250 fake"); err != nil {
		return
	}
	var body []string
	inData := false
	for {
		line, err = readLine(r, maxLine)
		if err != nil {
			return
		}
		if inData {
			if line == "." {
				f.mu.Lock()
				f.last.body = append([]string(nil), body...)
				f.last.data = true
				f.mu.Unlock()
				if f.dropData {
					return
				}
				code := f.dataCode
				if code == 0 {
					code = 250
				}
				text := "ok"
				if code/100 != 2 {
					text = "rejected"
				}
				if err := writeLine(w, strconv.Itoa(code)+" "+text); err != nil {
					return
				}
				select {
				case f.done <- struct{}{}:
				default:
				}
				inData = false
				body = nil
				continue
			}
			body = append(body, line)
			continue
		}
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "MAIL "):
			f.mu.Lock()
			f.last.mail = line
			f.mu.Unlock()
			if err := writeLine(w, "250 ok"); err != nil {
				return
			}
		case strings.HasPrefix(upper, "RCPT "):
			addr := between(line, '<', '>')
			f.mu.Lock()
			f.last.rcpts = append(f.last.rcpts, addr)
			f.mu.Unlock()
			code := 250
			if f.rcptCode != nil {
				code = f.rcptCode(addr)
			}
			reply := "250 ok"
			if code != 250 {
				reply = strconv.Itoa(code) + " 5.1.1 user unknown"
			}
			if err := writeLine(w, reply); err != nil {
				return
			}
		case upper == "DATA":
			if err := writeLine(w, "354 go"); err != nil {
				return
			}
			inData = true
		case upper == "RSET" || upper == "NOOP":
			if err := writeLine(w, "250 ok"); err != nil {
				return
			}
		case upper == "QUIT":
			_ = writeLine(w, "221 bye")
			return
		default:
			if err := writeLine(w, "500 no"); err != nil {
				return
			}
		}
	}
}

func (f *fakeUp) snapshot() txn {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.last
	out.rcpts = append([]string(nil), f.last.rcpts...)
	out.body = append([]string(nil), f.last.body...)
	return out
}

func (f *fakeUp) wait(t *testing.T) txn {
	t.Helper()
	select {
	case <-f.done:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream did not finish the message")
	}
	return f.snapshot()
}

func between(line string, open, close byte) string {
	start := strings.IndexByte(line, open)
	end := strings.IndexByte(line, close)
	if start < 0 || end < start {
		return ""
	}
	return line[start+1 : end]
}

func mustCIDR(t *testing.T, raw string) (net.IP, *net.IPNet) {
	t.Helper()
	ip, network, err := net.ParseCIDR(raw)
	if err != nil {
		t.Fatal(err)
	}
	return ip, network
}
