package mailbox

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

type testServerOptions struct {
	authFail, badTLS, writable, zeroValidity, hugeLiteral, stall bool
	count                                                        uint32
	oversized                                                    bool
	id                                                           bool
}

func mockMailbox(t *testing.T, opts testServerOptions) (*Client, *[]string, *sync.Mutex) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "mail.example.com"}, DNSNames: []string{"mail.example.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, certTemplate, certTemplate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyBytes, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes}))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	var commands []string
	t.Cleanup(func() {
		_ = listener.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
			go serveMockMailbox(conn, opts, &commands, &mu)
		}
	}()
	cl, err := NewClient(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(parsed)
	if !opts.badTLS {
		cl.roots = pool
	}
	cl.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	}
	cl.dialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "8.8.8.8:993" {
			t.Error("unexpected unpinned address")
		}
		return (&net.Dialer{}).DialContext(ctx, network, listener.Addr().String())
	}
	return cl, &commands, &mu
}

func serveMockMailbox(conn net.Conn, opts testServerOptions, commands *[]string, mu *sync.Mutex) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
	fmt.Fprint(conn, "* OK mock mailbox ready\r\n")
	r := bufio.NewReader(conn)
	const raw = "From: =?UTF-8?B?5ZCI5oiQ5Y+R5Lu25Lq6?= <sender@example.net>\r\nTo: synthetic@example.com\r\nSubject: =?UTF-8?B?5Lya6K6u5a6J5o6S?=\r\nDate: Thu, 1 Jan 2026 00:00:00 +0000\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nsynthetic message body"
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			return
		}
		tag := parts[0]
		name := strings.ToUpper(parts[1])
		mu.Lock()
		*commands = append(*commands, strings.TrimSpace(line))
		mu.Unlock()
		switch name {
		case "LOGIN":
			if opts.stall {
				_, _ = r.ReadByte()
				return
			}
			if opts.authFail {
				fmt.Fprintf(conn, "%s NO sensitive-upstream-password-%s\r\n", tag, "synthetic-app-code")
			} else {
				fmt.Fprintf(conn, "%s OK logged in\r\n", tag)
			}
		case "CAPABILITY":
			caps := "IMAP4rev1"
			if opts.id {
				caps += " ID"
			}
			fmt.Fprintf(conn, "* CAPABILITY %s\r\n%s OK capabilities\r\n", caps, tag)
		case "ID":
			fmt.Fprintf(conn, "* ID (\"name\" \"Mock Server\")\r\n%s OK client accepted\r\n", tag)
		case "EXAMINE":
			validity := uint32(123)
			if opts.zeroValidity {
				validity = 0
			}
			mode := "READ-ONLY"
			if opts.writable {
				mode = "READ-WRITE"
			}
			fmt.Fprintf(conn, "* FLAGS (\\Seen)\r\n* %d EXISTS\r\n* OK [UIDVALIDITY %d] stable\r\n%s OK [%s] examined\r\n", opts.count, validity, tag, mode)
		case "FETCH":
			size := len(raw)
			if opts.oversized {
				size = MaxRawMessageBytes + 1
			}
			if opts.count > 0 {
				fmt.Fprintf(conn, "* %d FETCH (UID 77 RFC822.SIZE %d FLAGS (\\Seen) INTERNALDATE \"01-Jan-2026 00:00:00 +0000\")\r\n", opts.count, size)
			}
			fmt.Fprintf(conn, "%s OK fetched metadata\r\n", tag)
		case "UID":
			if len(parts) < 3 || strings.ToUpper(parts[2]) != "FETCH" {
				return
			}
			if opts.hugeLiteral {
				fmt.Fprintf(conn, "* 1 FETCH (UID 77 BODY[] {4294967295}\r\n")
				continue
			}
			fmt.Fprintf(conn, "* %d FETCH (UID 77 BODY[] {%d}\r\n%s)\r\n%s OK fetched body\r\n", opts.count, len(raw), raw, tag)
		default:
			fmt.Fprintf(conn, "%s BAD unapproved command\r\n", tag)
		}
	}
}

func TestTLSReadOnlyProbeAndFetch(t *testing.T) {
	cl, commands, mu := mockMailbox(t, testServerOptions{count: 1, id: true})
	info, err := cl.Probe(context.Background())
	if err != nil || !info.ReadOnly || info.UIDValidity != 123 || info.AccountID != testConfig().Username {
		t.Fatalf("probe=%+v err=%v", info, err)
	}
	batch, err := cl.Fetch(context.Background())
	if err != nil || batch.Skipped != 0 || len(batch.Mails) != 1 || batch.Truncated {
		t.Fatalf("fetch=%+v err=%v", batch, err)
	}
	m := batch.Mails[0]
	if m.ExternalID != "123:77" || m.UID != 77 || !m.Seen || m.Body != "synthetic message body" || m.Subject != "会议安排" || m.FromAddress != "sender@example.net" {
		t.Fatalf("unexpected synthetic mail %+v", m)
	}
	mu.Lock()
	defer mu.Unlock()
	foundPeek, foundID := false, false
	for _, cmd := range *commands {
		fields := strings.Fields(cmd)
		name := strings.ToUpper(fields[1])
		switch name {
		case "LOGIN", "CAPABILITY", "EXAMINE":
		case "ID":
			foundID = true
			if strings.Contains(cmd, testConfig().Username) || strings.Contains(cmd, testConfig().Password) {
				t.Fatal("ID leaks user info")
			}
		case "FETCH":
			if strings.Contains(strings.ToUpper(cmd), "BODY") {
				t.Fatal("body fetched without UID")
			}
		case "UID":
			if !strings.Contains(cmd, "BODY.PEEK[]") {
				t.Fatal("body read can mark seen")
			}
			foundPeek = true
		default:
			t.Fatalf("unexpected mutation command %s", cmd)
		}
	}
	if !foundPeek || !foundID {
		t.Fatal("read-only body or ID command missing")
	}
}

func TestTLSAuthMailboxAndCancelFailures(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options testServerOptions
		kind    Kind
	}{{"bad certificate", testServerOptions{badTLS: true}, TLSFailed}, {"auth failure", testServerOptions{authFail: true}, AuthFailed}, {"writable examine", testServerOptions{writable: true}, ProtocolError}, {"zero UID validity", testServerOptions{zeroValidity: true}, ProtocolError}} {
		t.Run(tc.name, func(t *testing.T) {
			cl, _, _ := mockMailbox(t, tc.options)
			info, err := cl.Probe(context.Background())
			expectKind(t, err, tc.kind)
			if info != (Info{}) {
				t.Fatal("failed probe returns mailbox info")
			}
			if strings.Contains(err.Error(), "synthetic-app-code") || strings.Contains(err.Error(), "sensitive-upstream") {
				t.Fatal("raw upstream error leaked")
			}
		})
	}
	cl, _, _ := mockMailbox(t, testServerOptions{stall: true})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := cl.Probe(ctx)
	if err == nil || time.Since(start) > time.Second {
		t.Fatal("canceled mailbox call did not stop promptly")
	}
}

func TestFetchLimitsAndNoFalseCompleteness(t *testing.T) {
	cl, _, _ := mockMailbox(t, testServerOptions{count: 150, oversized: true})
	cl.cfg.Limit = 50
	batch, err := cl.Fetch(context.Background())
	if err != nil || !batch.Truncated || batch.Skipped != 50 || len(batch.Mails) != 0 {
		t.Fatalf("oversize/missing handling %+v %v", batch, err)
	}
	cl, _, _ = mockMailbox(t, testServerOptions{count: 1, hugeLiteral: true})
	batch, err = cl.Fetch(context.Background())
	expectKind(t, err, ProtocolError)
	if len(batch.Mails) > 0 || batch.Info != (Info{}) {
		t.Fatal("malicious literal left a partial batch")
	}
	cl, _, _ = mockMailbox(t, testServerOptions{})
	batch, err = cl.Fetch(context.Background())
	if err != nil || len(batch.Mails) != 0 || batch.Skipped != 0 {
		t.Fatal("empty mailbox incorrectly reported")
	}
}
