package mailbox

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// wireConn validates literal declarations before go-imap can allocate them.
// Literal payloads are streamed by exact length and never scanned as commands.
// Read budgets also bound untagged/noisy responses and overlong protocol lines.
type wireConn struct {
	net.Conn
	reader    *bufio.Reader
	line      []byte
	literal   int64
	remaining int64
	deadline  time.Time
	depth     int
}

func newWireConn(conn net.Conn, budget int64, deadline time.Time) *wireConn {
	return &wireConn{Conn: conn, reader: bufio.NewReaderSize(conn, 64<<10), remaining: budget, deadline: deadline}
}

func (c *wireConn) SetDeadline(at time.Time) error {
	if at.IsZero() || at.After(c.deadline) {
		at = c.deadline
	}
	return c.Conn.SetDeadline(at)
}

func (c *wireConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if c.remaining <= 0 {
		_ = c.Conn.Close()
		return 0, errWireLimit
	}
	if len(c.line) > 0 {
		n := copy(p, c.line)
		c.line = c.line[n:]
		c.remaining -= int64(n)
		return n, nil
	}
	if c.literal > 0 {
		if int64(len(p)) > c.literal {
			p = p[:c.literal]
		}
		if int64(len(p)) > c.remaining {
			p = p[:c.remaining]
		}
		n, err := c.reader.Read(p)
		c.literal -= int64(n)
		c.remaining -= int64(n)
		return n, err
	}
	line, err := c.reader.ReadSlice('\n')
	if err != nil {
		_ = c.Conn.Close()
		if err == bufio.ErrBufferFull {
			return 0, errWireLimit
		}
		return 0, err
	}
	if int64(len(line)) > c.remaining || !bytes.HasSuffix(line, []byte("\r\n")) {
		_ = c.Conn.Close()
		return 0, errWireLimit
	}
	trimmed := strings.TrimSuffix(string(line), "\r\n")
	depth, quoted, escaped := c.depth, false, false
	for _, ch := range trimmed {
		if ch == '"' && !escaped {
			quoted = !quoted
		}
		if !quoted {
			if ch == '(' {
				depth++
				if depth > 64 {
					_ = c.Conn.Close()
					return 0, errWireLimit
				}
			}
			if ch == ')' && depth > 0 {
				depth--
			}
		}
		if ch == '\\' && !escaped {
			escaped = true
		} else {
			escaped = false
		}
	}
	announcedLiteral := false
	if strings.HasSuffix(trimmed, "}") {
		if start := strings.LastIndexByte(trimmed, '{'); start >= 0 {
			amount := strings.TrimSuffix(trimmed[start+1:len(trimmed)-1], "+")
			length, err := strconv.ParseUint(amount, 10, 32)
			if err != nil || length > MaxRawMessageBytes || int64(length) > c.remaining-int64(len(line)) {
				_ = c.Conn.Close()
				return 0, errWireLimit
			}
			c.literal = int64(length)
			announcedLiteral = true
		}
	}
	// IMAP lists may span zero/nonzero literal boundaries. Keep cumulative
	// depth until a response's terminating non-literal line, not per line.
	if announcedLiteral {
		c.depth = depth
	} else {
		c.depth = 0
	}
	c.line = append(c.line[:0], line...)
	return c.Read(p)
}

var _ io.Reader = (*wireConn)(nil)
