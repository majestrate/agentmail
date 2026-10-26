// Package milter speaks the Sendmail milter protocol Postfix dials for the
// inbound SMTP listener. The process is plaintext. The pod sidecar
// authenticates Postfix with mutual TLS.
package milter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"time"

	"agentmail/internal/directory"
)

const (
	// commandTimeout matches Postfix milter_command_timeout.
	commandTimeout = 30 * time.Second
	// maxFrame is above the libmilter chunk size. A larger frame closes the
	// connection instead of buffering it.
	maxFrame = 1 << 20
	// protocolVersion is Sendmail milter protocol 6, which Postfix speaks.
	protocolVersion = 6

	cmdAbort   = 'A'
	cmdBody    = 'B'
	cmdConnect = 'C'
	cmdMacro   = 'D'
	cmdBodyEOB = 'E'
	cmdHelo    = 'H'
	cmdQuitNC  = 'K'
	cmdHeader  = 'L'
	cmdMail    = 'M'
	cmdEOH     = 'N'
	cmdOptNeg  = 'O'
	cmdQuit    = 'Q'
	cmdRcpt    = 'R'
	cmdData    = 'T'
	cmdUnknown = 'U'

	repAccept   = 'a'
	repContinue = 'c'
	repTempfail = 't'
	repReply    = 'y'
)

// Decider accepts or rejects one envelope party. A *RejectError is a 550.
// Any other error is a 451.
type Decider interface {
	Sender(ctx context.Context, address string) error
	Recipient(ctx context.Context, address string) error
}

// Serve accepts milter clients. Postfix dials this with smtpd_milters=inet:host:port.
func Serve(ctx context.Context, ln net.Listener, decider Decider) error {
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
		go serveConn(ctx, conn, decider)
	}
}

type message struct {
	sender     string
	haveMail   bool
	rcpts      []string
	bytes      int
	tempFailed bool
}

func serveConn(ctx context.Context, conn net.Conn, decider Decider) {
	defer conn.Close()
	reader := bufio.NewReaderSize(conn, 64*1024)
	writer := bufio.NewWriterSize(conn, 4096)
	negotiated := false
	var msg message
	for {
		if err := conn.SetReadDeadline(time.Now().Add(commandTimeout)); err != nil {
			return
		}
		cmd, data, err := readFrame(reader)
		if err != nil {
			if !errors.Is(err, io.EOF) && ctx.Err() == nil {
				log.Printf("milter read: %v", err)
			}
			return
		}
		if !negotiated && cmd != cmdOptNeg {
			log.Printf("milter command %q before negotiation", cmd)
			return
		}
		switch cmd {
		case cmdOptNeg:
			if negotiated {
				return
			}
			payload, err := negotiate(data)
			if err != nil {
				log.Printf("milter negotiate: %v", err)
				return
			}
			if err := writeFrame(writer, cmdOptNeg, payload); err != nil {
				return
			}
			negotiated = true
		case cmdMacro, cmdAbort:
			if cmd == cmdAbort {
				msg = message{}
			}
			continue
		case cmdQuit:
			return
		case cmdQuitNC:
			msg = message{}
			continue
		case cmdMail:
			msg = message{}
			if err := handleMail(ctx, &msg, decider, data); err != nil {
				noteTemp(&msg, err)
				if !writeResult(writer, "sender", "", err) {
					return
				}
				continue
			}
			msg.haveMail = true
			if err := writeFrame(writer, repContinue, nil); err != nil {
				return
			}
		case cmdRcpt:
			addr, err := handleRcpt(ctx, &msg, decider, data)
			if err != nil {
				noteTemp(&msg, err)
				if !writeResult(writer, "recipient", addr, err) {
					return
				}
				continue
			}
			msg.rcpts = append(msg.rcpts, addr)
			if err := writeFrame(writer, repContinue, nil); err != nil {
				return
			}
		case cmdBody:
			msg.bytes += len(data)
			if err := writeFrame(writer, repContinue, nil); err != nil {
				return
			}
		case cmdBodyEOB:
			msg.bytes += len(data)
			if err := finishMessage(&msg); err != nil {
				if !writeResult(writer, "message", msg.sender, err) {
					return
				}
				msg = message{}
				continue
			}
			logAccept(&msg)
			if err := writeFrame(writer, repAccept, nil); err != nil {
				return
			}
			msg = message{}
		case cmdConnect, cmdHelo, cmdHeader, cmdEOH, cmdData, cmdUnknown:
			if err := writeFrame(writer, repContinue, nil); err != nil {
				return
			}
		default:
			// Protocol 0 expects a reply for every command except macros,
			// abort, and quit. An unknown byte still gets continue so the
			// session stays aligned.
			if err := writeFrame(writer, repContinue, nil); err != nil {
				return
			}
		}
		if err := writer.Flush(); err != nil {
			return
		}
	}
}

func handleMail(ctx context.Context, msg *message, decider Decider, data []byte) error {
	addr, err := envelopeAddress(data)
	if err != nil {
		return err
	}
	if err := decider.Sender(ctx, addr); err != nil {
		return err
	}
	msg.sender = addr
	return nil
}

func handleRcpt(ctx context.Context, msg *message, decider Decider, data []byte) (string, error) {
	addr, err := envelopeAddress(data)
	if err != nil {
		return "", err
	}
	if !msg.haveMail {
		return addr, reject("sender rejected")
	}
	if addr == "" {
		return "", reject("malformed address")
	}
	if err := decider.Recipient(ctx, addr); err != nil {
		return addr, err
	}
	return addr, nil
}

func noteTemp(msg *message, err error) {
	var rejected *RejectError
	if !errors.As(err, &rejected) {
		msg.tempFailed = true
	}
}

func finishMessage(msg *message) error {
	if msg.haveMail && len(msg.rcpts) > 0 {
		return nil
	}
	// A lookup failure must stay a 451. A later end-of-body with no accepted
	// recipient would otherwise turn that temporary error into a 550.
	if msg.tempFailed {
		return fmt.Errorf("recipient lookup failed")
	}
	return reject("no valid recipients")
}

func logAccept(msg *message) {
	log.Printf("milter accept from=%s to=%s bytes=%d", showSender(msg.sender), strings.Join(msg.rcpts, ","), msg.bytes)
}

func showSender(addr string) string {
	if addr == "" {
		return "<>"
	}
	return addr
}

// writeResult sends a 550 for *RejectError and a tempfail otherwise.
// It returns false when the write fails.
func writeResult(w *bufio.Writer, what, addr string, err error) bool {
	var rejected *RejectError
	if errors.As(err, &rejected) {
		line := "550 5.7.1 " + rejected.Reason
		log.Printf("milter reject %s address=%s reply=%s", what, showSender(addr), line)
		if writeErr := writeFrame(w, repReply, append([]byte(line), 0)); writeErr != nil {
			return false
		}
		return w.Flush() == nil
	}
	log.Printf("milter tempfail %s address=%s: %v", what, showSender(addr), err)
	if writeErr := writeFrame(w, repTempfail, nil); writeErr != nil {
		return false
	}
	return w.Flush() == nil
}

// negotiate answers protocol 6 with no message modifications and no skipped
// stages. The length prefix is added by writeFrame. data is version, actions,
// and protocol flags, each a uint32 in network order.
func negotiate(data []byte) ([]byte, error) {
	if len(data) < 12 {
		return nil, fmt.Errorf("short option negotiation")
	}
	version := binary.BigEndian.Uint32(data[0:4])
	if version < 2 {
		return nil, fmt.Errorf("milter version %d", version)
	}
	if version > protocolVersion {
		version = protocolVersion
	}
	out := make([]byte, 12)
	binary.BigEndian.PutUint32(out[0:4], version)
	// actions stay 0: accept, reject, and tempfail only.
	// protocol flags stay 0: Postfix sends every stage and waits for a reply.
	binary.BigEndian.PutUint32(out[4:8], 0)
	binary.BigEndian.PutUint32(out[8:12], 0)
	return out, nil
}

// envelopeAddress reads the first NUL-terminated envelope token. Postfix
// sends <addr> or <>. An empty result is the null sender. Anything else must
// be a bare address.
func envelopeAddress(data []byte) (string, error) {
	raw, ok := cstring(data)
	if !ok {
		return "", reject("malformed address")
	}
	raw = strings.TrimSpace(raw)
	if len(raw) >= 2 && raw[0] == '<' && raw[len(raw)-1] == '>' {
		raw = strings.TrimSpace(raw[1 : len(raw)-1])
	}
	if raw == "" {
		return "", nil
	}
	addr, err := directory.CanonicalAddress(raw)
	if err != nil {
		return "", reject("malformed address")
	}
	return addr, nil
}

func cstring(data []byte) (string, bool) {
	i := bytes.IndexByte(data, 0)
	if i < 0 {
		return "", false
	}
	return string(data[:i]), true
}

// readFrame reads one milter packet. The uint32 length includes the command
// byte and excludes itself, and it is network byte order.
func readFrame(r io.Reader) (byte, []byte, error) {
	var n uint32
	if err := binary.Read(r, binary.BigEndian, &n); err != nil {
		return 0, nil, err
	}
	if n == 0 || n > maxFrame {
		return 0, nil, fmt.Errorf("milter frame length %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, nil, err
	}
	return buf[0], buf[1:], nil
}

func writeFrame(w io.Writer, cmd byte, data []byte) error {
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(1+len(data)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := w.Write([]byte{cmd}); err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	_, err := w.Write(data)
	return err
}
