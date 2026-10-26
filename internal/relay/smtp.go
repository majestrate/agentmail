package relay

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

const (
	maxCommand = 8192
	maxLine    = 128 * 1024
	maxReply   = 8192
)

type smtpError struct {
	code   int
	text   string
	closed bool
}

func (e *smtpError) Error() string {
	if e.text == "" {
		return strconv.Itoa(e.code)
	}
	return strconv.Itoa(e.code) + " " + e.text
}

func replyErr(code int, text string) error {
	return &smtpError{code: code, text: text}
}

func readLine(r *bufio.Reader, max int) (string, error) {
	var buf []byte
	for {
		frag, err := r.ReadSlice('\n')
		buf = append(buf, frag...)
		if len(buf) > max {
			return "", replyErr(500, "5.5.2 line too long")
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			return "", err
		}
		break
	}
	line := string(buf)
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	return line, nil
}

func writeLine(w *bufio.Writer, line string) error {
	if _, err := w.WriteString(line); err != nil {
		return err
	}
	if _, err := w.WriteString("\r\n"); err != nil {
		return err
	}
	return w.Flush()
}

// readReply reads one SMTP reply. Multiline text is joined with spaces.
func readReply(r *bufio.Reader) (int, string, error) {
	var parts []string
	code := 0
	for {
		line, err := readLine(r, maxReply)
		if err != nil {
			return 0, "", err
		}
		if len(line) < 3 {
			return 0, "", fmt.Errorf("short smtp reply")
		}
		n, err := strconv.Atoi(line[:3])
		if err != nil || n < 100 || n > 599 {
			return 0, "", fmt.Errorf("bad smtp reply")
		}
		if code == 0 {
			code = n
		} else if n != code {
			return 0, "", fmt.Errorf("smtp reply code changed")
		}
		if len(line) > 4 {
			parts = append(parts, line[4:])
		}
		if len(line) == 3 || line[3] != '-' {
			return code, strings.Join(parts, " "), nil
		}
	}
}

func writeReply(w *bufio.Writer, code int, text string) error {
	text = strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.TrimSpace(text))
	if text == "" {
		text = "ok"
	}
	return writeLine(w, strconv.Itoa(code)+" "+text)
}

// parsePath splits an SMTP path argument into the address inside <> and the
// parameter list. An empty address is the null sender.
func parsePath(arg string) (string, []string, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" || arg[0] != '<' {
		return "", nil, replyErr(501, "5.5.4 malformed address")
	}
	end := strings.IndexByte(arg, '>')
	if end < 0 {
		return "", nil, replyErr(501, "5.5.4 malformed address")
	}
	addr := arg[1:end]
	rest := strings.TrimSpace(arg[end+1:])
	var params []string
	if rest != "" {
		params = strings.Fields(rest)
	}
	return addr, params, nil
}

// forwardParams keeps SIZE and BODY. Other parameters are dropped so a
// client cannot inject a second SMTP command.
func forwardParams(params []string, maxSize int64) ([]string, error) {
	var out []string
	for _, param := range params {
		key, val, ok := strings.Cut(param, "=")
		if !ok || key == "" || val == "" || strings.ContainsAny(param, "\r\n ") {
			return nil, replyErr(501, "5.5.4 malformed parameter")
		}
		switch strings.ToUpper(key) {
		case "SIZE":
			n, err := strconv.ParseInt(val, 10, 64)
			if err != nil || n < 0 {
				return nil, replyErr(501, "5.5.4 malformed parameter")
			}
			if maxSize > 0 && n > maxSize {
				return nil, replyErr(552, "5.3.4 message too large")
			}
			out = append(out, "SIZE="+val)
		case "BODY":
			body := strings.ToUpper(val)
			if body != "7BIT" && body != "8BITMIME" {
				return nil, replyErr(501, "5.5.4 malformed parameter")
			}
			out = append(out, "BODY="+body)
		}
	}
	return out, nil
}

func pathOf(addr string) string {
	if addr == "" {
		return "<>"
	}
	return "<" + addr + ">"
}
