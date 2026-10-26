package relay

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

type upstream struct {
	conn net.Conn
	r    *bufio.Reader
	w    *bufio.Writer
}

func dialUpstream(ctx context.Context, cfg Config, addr string) (*upstream, error) {
	dial := cfg.Dial
	if dial == nil {
		dialer := net.Dialer{Timeout: cfg.timeout()}
		dial = dialer.DialContext
	}
	conn, err := dial(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	u := &upstream{
		conn: conn,
		r:    bufio.NewReaderSize(conn, 64*1024),
		w:    bufio.NewWriterSize(conn, 64*1024),
	}
	if err := conn.SetDeadline(time.Now().Add(cfg.timeout())); err != nil {
		conn.Close()
		return nil, err
	}
	code, _, err := readReply(u.r)
	if err != nil || code != 220 {
		conn.Close()
		if err == nil {
			err = fmt.Errorf("upstream greeting %d", code)
		}
		return nil, err
	}
	if err := writeLine(u.w, "EHLO "+cfg.hostname()); err != nil {
		conn.Close()
		return nil, err
	}
	code, _, err = readReply(u.r)
	if err != nil || code/100 != 2 {
		conn.Close()
		if err == nil {
			err = fmt.Errorf("upstream ehlo %d", code)
		}
		return nil, err
	}
	return u, nil
}

func (u *upstream) Close() {
	if u == nil || u.conn == nil {
		return
	}
	_ = u.conn.Close()
}

func (u *upstream) touch(timeout time.Duration) {
	_ = u.conn.SetDeadline(time.Now().Add(timeout))
}

func (u *upstream) mail(from string, params []string) (int, string, error) {
	line := "MAIL FROM:" + pathOf(from)
	if len(params) > 0 {
		line += " " + strings.Join(params, " ")
	}
	if err := writeLine(u.w, line); err != nil {
		return 0, "", err
	}
	return readReply(u.r)
}

func (u *upstream) rcpt(addr string) (int, string, error) {
	if err := writeLine(u.w, "RCPT TO:"+pathOf(addr)); err != nil {
		return 0, "", err
	}
	return readReply(u.r)
}

func (u *upstream) data() (int, string, error) {
	if err := writeLine(u.w, "DATA"); err != nil {
		return 0, "", err
	}
	return readReply(u.r)
}

// writeBodyLine writes one unstuffed body line, stuffing a leading dot.
func (u *upstream) writeBodyLine(line string) error {
	if strings.HasPrefix(line, ".") {
		line = "." + line
	}
	_, err := u.w.WriteString(line + "\r\n")
	return err
}

func (u *upstream) finish() (int, string, error) {
	if _, err := u.w.WriteString(".\r\n"); err != nil {
		return 0, "", err
	}
	if err := u.w.Flush(); err != nil {
		return 0, "", err
	}
	return readReply(u.r)
}
