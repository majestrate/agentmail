package relay

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	"agentmail/internal/directory"
)

const (
	dirInbound  = "inbound"
	dirOutbound = "outbound"
)

// Config is one listener. Inbound accepts recipients in Domains and dials
// Upstream. Outbound refuses recipients in Domains and dials Upstream.
type Config struct {
	Hostname  string
	Domains   []string
	Upstream  string
	Allow     []*net.IPNet
	Outbound  bool
	MaxSize   int64
	Timeout   time.Duration
	Dial      func(ctx context.Context, network, addr string) (net.Conn, error)
	now       func() time.Time
	messageID func() string
}

func (c Config) timeout() time.Duration {
	if c.Timeout <= 0 {
		return 60 * time.Second
	}
	return c.Timeout
}

func (c Config) hostname() string {
	if c.Hostname == "" {
		return "localhost"
	}
	return c.Hostname
}

func (c Config) direction() string {
	if c.Outbound {
		return dirOutbound
	}
	return dirInbound
}

func (c Config) allows(ip net.IP) bool {
	if len(c.Allow) == 0 {
		return true
	}
	for _, n := range c.Allow {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func (c Config) local(addr string) bool {
	at := strings.LastIndex(addr, "@")
	if at <= 0 || at == len(addr)-1 {
		return false
	}
	domain := strings.ToLower(addr[at+1:])
	for _, candidate := range c.Domains {
		if domain == strings.ToLower(candidate) {
			return true
		}
	}
	return false
}

func (c Config) acceptRecipient(addr string) error {
	local := c.local(addr)
	if c.Outbound && local {
		return replyErr(550, "5.7.1 recipient domain refused")
	}
	if !c.Outbound && !local {
		return replyErr(550, "5.7.1 recipient domain rejected")
	}
	return nil
}

// Serve accepts SMTP clients and proxies each transaction to Upstream.
// A 250 for the message is returned only after the upstream returns 250.
func Serve(ctx context.Context, ln net.Listener, cfg Config) error {
	if cfg.Upstream == "" {
		return errors.New("relay upstream is required")
	}
	domains, err := normalizeDomains(cfg.Domains)
	if err != nil {
		return err
	}
	cfg.Domains = domains
	if cfg.MaxSize <= 0 {
		cfg.MaxSize = 50 << 20
	}
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
		go serveConn(ctx, conn, cfg)
	}
}

type session struct {
	ctx    context.Context
	conn   net.Conn
	r      *bufio.Reader
	w      *bufio.Writer
	cfg    Config
	helo   string
	from   string
	mail   bool
	rcpts  []string
	params []string
	up     *upstream
}

func serveConn(ctx context.Context, conn net.Conn, cfg Config) {
	defer conn.Close()
	s := &session{
		ctx:  ctx,
		conn: conn,
		r:    bufio.NewReaderSize(conn, 64*1024),
		w:    bufio.NewWriterSize(conn, 64*1024),
		cfg:  cfg,
	}
	defer s.closeUpstream()
	if !s.clientAllowed() {
		_ = s.reply(550, "5.7.1 client host rejected")
		log.Printf("relay %s reject client=%s", cfg.direction(), conn.RemoteAddr())
		return
	}
	if err := s.touch(); err != nil {
		return
	}
	if err := s.reply(220, cfg.hostname()+" ESMTP"); err != nil {
		return
	}
	for {
		if err := s.touch(); err != nil {
			return
		}
		line, err := readLine(s.r, maxCommand)
		if err != nil {
			return
		}
		if err := s.command(line); err != nil {
			var smtp *smtpError
			if errors.As(err, &smtp) {
				if smtp.closed {
					return
				}
				if replyErr := s.reply(smtp.code, smtp.text); replyErr != nil {
					return
				}
				continue
			}
			if errors.Is(err, io.EOF) {
				return
			}
			log.Printf("relay %s client=%s: %v", cfg.direction(), conn.RemoteAddr(), err)
			_ = s.reply(450, "4.7.1 upstream unavailable")
			return
		}
	}
}

func (s *session) clientAllowed() bool {
	host, _, err := net.SplitHostPort(s.conn.RemoteAddr().String())
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return s.cfg.allows(ip)
}

func (s *session) touch() error {
	return s.conn.SetDeadline(time.Now().Add(s.cfg.timeout()))
}

func (s *session) reply(code int, text string) error {
	return writeReply(s.w, code, text)
}

func (s *session) closeUpstream() {
	if s.up != nil {
		s.up.Close()
		s.up = nil
	}
}

func (s *session) reset() {
	s.closeUpstream()
	s.from = ""
	s.mail = false
	s.rcpts = nil
	s.params = nil
}

func (s *session) command(line string) error {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	verb, arg, _ := strings.Cut(line, " ")
	switch strings.ToUpper(verb) {
	case "EHLO", "HELO":
		return s.heloCommand(verb, strings.TrimSpace(arg))
	case "MAIL":
		return s.mailCommand(arg)
	case "RCPT":
		return s.rcptCommand(arg)
	case "DATA":
		return s.dataCommand()
	case "RSET":
		s.reset()
		return s.reply(250, "2.0.0 ok")
	case "NOOP":
		return s.reply(250, "2.0.0 ok")
	case "QUIT":
		_ = s.reply(221, "2.0.0 bye")
		return &smtpError{closed: true}
	case "VRFY", "EXPN", "STARTTLS", "AUTH":
		return replyErr(502, "5.5.1 command not implemented")
	default:
		return replyErr(500, "5.5.2 command unrecognized")
	}
}

func (s *session) heloCommand(verb, arg string) error {
	if arg == "" || strings.ContainsAny(arg, "\r\n ") {
		return replyErr(501, "5.5.4 helo argument required")
	}
	s.reset()
	s.helo = arg
	if strings.EqualFold(verb, "EHLO") {
		if err := writeLine(s.w, "250-"+s.cfg.hostname()); err != nil {
			return err
		}
		if err := writeLine(s.w, "250-8BITMIME"); err != nil {
			return err
		}
		return writeLine(s.w, "250 SIZE "+itoa(s.cfg.MaxSize))
	}
	return s.reply(250, s.cfg.hostname())
}

func (s *session) mailCommand(arg string) error {
	if s.helo == "" {
		return replyErr(503, "5.5.1 send HELO first")
	}
	if s.mail {
		return replyErr(503, "5.5.1 nested MAIL command")
	}
	fromArg, ok := strings.CutPrefix(strings.TrimSpace(arg), "FROM:")
	if !ok {
		return replyErr(501, "5.5.4 malformed address")
	}
	raw, params, err := parsePath(fromArg)
	if err != nil {
		return err
	}
	forwarded, err := forwardParams(params, s.cfg.MaxSize)
	if err != nil {
		return err
	}
	from := ""
	if strings.TrimSpace(raw) != "" {
		from, err = directory.CanonicalAddress(raw)
		if err != nil {
			return replyErr(501, "5.5.4 malformed address")
		}
	}
	up, err := dialUpstream(s.ctx, s.cfg, s.cfg.Upstream)
	if err != nil {
		log.Printf("relay %s upstream %s: %v", s.cfg.direction(), s.cfg.Upstream, err)
		return replyErr(450, "4.7.1 upstream unavailable")
	}
	s.up = up
	s.up.touch(s.cfg.timeout())
	code, text, err := s.up.mail(from, forwarded)
	if err != nil {
		s.reset()
		return replyErr(450, "4.7.1 upstream unavailable")
	}
	if code/100 != 2 {
		s.reset()
		return replyErr(code, fallback(text, "sender rejected"))
	}
	s.from = from
	s.mail = true
	s.params = forwarded
	return s.reply(250, "2.1.0 ok")
}

func (s *session) rcptCommand(arg string) error {
	if !s.mail {
		return replyErr(503, "5.5.1 send MAIL first")
	}
	toArg, ok := strings.CutPrefix(strings.TrimSpace(arg), "TO:")
	if !ok {
		return replyErr(501, "5.5.4 malformed address")
	}
	raw, _, err := parsePath(toArg)
	if err != nil {
		return err
	}
	if strings.TrimSpace(raw) == "" {
		return replyErr(501, "5.5.4 malformed address")
	}
	addr, err := directory.CanonicalAddress(raw)
	if err != nil {
		return replyErr(501, "5.5.4 malformed address")
	}
	if err := s.cfg.acceptRecipient(addr); err != nil {
		return err
	}
	s.up.touch(s.cfg.timeout())
	code, text, err := s.up.rcpt(addr)
	if err != nil {
		s.reset()
		return replyErr(450, "4.7.1 upstream unavailable")
	}
	if code/100 != 2 {
		return replyErr(code, fallback(text, "recipient rejected"))
	}
	s.rcpts = append(s.rcpts, addr)
	return s.reply(250, "2.1.5 ok")
}

func (s *session) dataCommand() error {
	if len(s.rcpts) == 0 {
		return replyErr(503, "5.5.1 send RCPT first")
	}
	s.up.touch(s.cfg.timeout())
	code, text, err := s.up.data()
	if err != nil {
		s.reset()
		return replyErr(450, "4.7.1 upstream unavailable")
	}
	if code != 354 {
		s.reset()
		return replyErr(code, fallback(text, "data rejected"))
	}
	if err := s.reply(354, "start mail input"); err != nil {
		return err
	}
	n, err := s.transferBody()
	if err != nil {
		var smtp *smtpError
		if errors.As(err, &smtp) {
			drained := s.drainBody()
			s.reset()
			if !drained {
				_ = s.reply(smtp.code, smtp.text)
				return &smtpError{closed: true}
			}
			return smtp
		}
		s.reset()
		_ = s.reply(450, "4.7.1 upstream unavailable")
		return &smtpError{closed: true}
	}
	s.up.touch(s.cfg.timeout())
	code, text, err = s.up.finish()
	from := s.from
	rcpts := append([]string(nil), s.rcpts...)
	s.reset()
	if err != nil {
		log.Printf("relay %s data from=%s to=%s: %v", s.cfg.direction(), show(from), strings.Join(rcpts, ","), err)
		return replyErr(450, "4.7.1 upstream unavailable")
	}
	log.Printf("relay %s from=%s to=%s status=%d bytes=%d", s.cfg.direction(), show(from), strings.Join(rcpts, ","), code, n)
	if code/100 != 2 {
		return replyErr(code, fallback(text, "message rejected"))
	}
	return s.reply(250, "2.0.0 ok")
}

func (s *session) transferBody() (int64, error) {
	var total int64
	for _, line := range s.received() {
		if err := s.up.writeBodyLine(line); err != nil {
			return total, err
		}
		total += int64(len(line) + 2)
	}
	for {
		if err := s.touch(); err != nil {
			return total, err
		}
		s.up.touch(s.cfg.timeout())
		line, err := readLine(s.r, maxLine)
		if err != nil {
			return total, err
		}
		if line == "." {
			return total, nil
		}
		if strings.HasPrefix(line, ".") {
			line = line[1:]
		}
		total += int64(len(line) + 2)
		if total > s.cfg.MaxSize {
			return total, replyErr(552, "5.3.4 message too large")
		}
		if err := s.up.writeBodyLine(line); err != nil {
			return total, err
		}
	}
}

func (s *session) drainBody() bool {
	for i := 0; i < 100000; i++ {
		_ = s.conn.SetDeadline(time.Now().Add(s.cfg.timeout()))
		line, err := readLine(s.r, maxLine)
		if err != nil {
			return false
		}
		if line == "." {
			return true
		}
	}
	return false
}

func (s *session) received() []string {
	now := time.Now
	if s.cfg.now != nil {
		now = s.cfg.now
	}
	id := s.messageID()
	ip := "unknown"
	if host, _, err := net.SplitHostPort(s.conn.RemoteAddr().String()); err == nil {
		ip = host
	}
	rcpt := ""
	if len(s.rcpts) > 0 {
		rcpt = " for <" + s.rcpts[0] + ">;"
	}
	return []string{
		"Received: from " + cleanToken(s.helo) + " ([" + ip + "])",
		"\tby " + s.cfg.hostname() + " with ESMTP id " + id + rcpt,
		"\t" + now().UTC().Format(time.RFC1123Z),
	}
}

func (s *session) messageID() string {
	if s.cfg.messageID != nil {
		return s.cfg.messageID()
	}
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "0"
	}
	return hex.EncodeToString(buf[:])
}

func show(addr string) string {
	if addr == "" {
		return "<>"
	}
	return addr
}

func fallback(text, def string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return def
	}
	return text
}

func cleanToken(raw string) string {
	clean := strings.Map(func(r rune) rune {
		if r > 32 && r < 127 && r != '(' && r != ')' && r != '\\' {
			return r
		}
		return -1
	}, raw)
	if clean == "" {
		return "unknown"
	}
	return clean
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
