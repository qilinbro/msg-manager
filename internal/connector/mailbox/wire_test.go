package mailbox

import (
	"bytes"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

type inputConn struct {
	io.Reader
	closed   bool
	deadline time.Time
}

func (c *inputConn) Write(p []byte) (int, error)         { return len(p), nil }
func (c *inputConn) Close() error                        { c.closed = true; return nil }
func (c *inputConn) LocalAddr() net.Addr                 { return &net.TCPAddr{} }
func (c *inputConn) RemoteAddr() net.Addr                { return &net.TCPAddr{} }
func (c *inputConn) SetDeadline(at time.Time) error      { c.deadline = at; return nil }
func (c *inputConn) SetReadDeadline(at time.Time) error  { return c.SetDeadline(at) }
func (c *inputConn) SetWriteDeadline(at time.Time) error { return c.SetDeadline(at) }

func TestWireReaderBoundsAndLiteralPayload(t *testing.T) {
	const payload = "{4294967295}\r\n(\"fake imap\")"
	wire := "* 1 FETCH (BODY[] {" + strconv.Itoa(len(payload)) + "}\r\n" + payload + ")\r\n"
	base := &inputConn{Reader: strings.NewReader(wire)}
	deadline := time.Now().Add(time.Second)
	guard := newWireConn(base, 1<<20, deadline)
	read, err := io.ReadAll(guard)
	if err != nil || string(read) != wire {
		t.Fatal("literal content was scanned as an IMAP line")
	}
	if guard.SetDeadline(time.Time{}) != nil || !base.deadline.Equal(deadline) {
		t.Fatal("overall timeout was cleared")
	}
	for _, input := range []string{"* 1 FETCH (BODY[] {4294967295}\r\n", "* 1 FETCH (BODY[] {" + strconv.Itoa(MaxRawMessageBytes+1) + "}\r\n", strings.Repeat("a", 70<<10) + "\r\n", strings.Repeat("(", 65) + "\r\n"} {
		base := &inputConn{Reader: strings.NewReader(input)}
		guard := newWireConn(base, 2<<20, deadline)
		if _, err := io.ReadAll(guard); err == nil || !base.closed {
			t.Fatal("unsafe protocol size accepted")
		}
	}
	for _, boundary := range []string{"{0}\r\n", "{3}\r\nabc"} {
		input := "* 1 FETCH " + strings.Repeat("(", 40) + boundary + strings.Repeat("(", 25) + "\r\n"
		base := &inputConn{Reader: strings.NewReader(input)}
		guard := newWireConn(base, 2<<20, deadline)
		if _, err := io.ReadAll(guard); err == nil || !base.closed {
			t.Fatal("cumulative list nesting bypassed literal boundary")
		}
	}
	base = &inputConn{Reader: bytes.NewReader([]byte("* OK ready\r\n"))}
	guard = newWireConn(base, 3, deadline)
	if _, err := io.ReadAll(guard); err == nil {
		t.Fatal("wire byte budget not enforced")
	}
}
