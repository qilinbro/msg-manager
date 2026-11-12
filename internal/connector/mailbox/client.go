package mailbox

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap"
)

const operationTimeout = 25 * time.Second

type Client struct {
	cfg         Config
	lookupIP    func(context.Context, string) ([]net.IPAddr, error)
	dialContext func(context.Context, string, string) (net.Conn, error)
	roots       *x509.CertPool
}

func NewClient(cfg Config) (*Client, error) {
	normalized, err := normalizedConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &Client{cfg: normalized, lookupIP: func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return net.DefaultResolver.LookupIPAddr(ctx, host)
	}, dialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext}, nil
}

func (c *Client) destination(ctx context.Context) (string, error) {
	var ips []net.IPAddr
	if ip, err := netip.ParseAddr(c.cfg.Host); err == nil {
		ips = []net.IPAddr{{IP: net.IP(ip.AsSlice())}}
	} else {
		var err error
		ips, err = c.lookupIP(ctx, c.cfg.Host)
		if err != nil {
			return "", fail(Unreachable)
		}
	}
	if len(ips) == 0 || len(ips) > 32 {
		return "", fail(AddressDenied)
	}
	for _, result := range ips {
		ip, ok := netip.AddrFromSlice(result.IP)
		if !ok || result.Zone != "" || !publicIP(ip) {
			return "", fail(AddressDenied)
		}
	}
	// Pin an address from this single validated resolution; no second DNS lookup.
	return net.JoinHostPort(ips[0].IP.String(), strconv.Itoa(c.cfg.Port)), nil
}

type session struct {
	client     *protocolClient
	conn       net.Conn
	stopCancel func() bool
	info       Info
}

func (s *session) close() { s.stopCancel(); _ = s.conn.Close() }

func (c *Client) open(ctx context.Context, budget int64) (*session, error) {
	destination, err := c.destination(ctx)
	if err != nil {
		return nil, err
	}
	conn, err := c.dialContext(ctx, "tcp", destination)
	if err != nil {
		return nil, fail(Unreachable)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	failClose := func(kind Kind) (*session, error) { stop(); _ = conn.Close(); return nil, fail(kind) }
	deadline, _ := ctx.Deadline()
	if conn.SetDeadline(deadline) != nil {
		return failClose(Unreachable)
	}
	tlsConn := tls.Client(conn, &tls.Config{ServerName: c.cfg.Host, RootCAs: c.roots, MinVersion: tls.VersionTLS12})
	if tlsConn.HandshakeContext(ctx) != nil {
		return failClose(TLSFailed)
	}
	guard := newWireConn(tlsConn, budget, deadline)
	cl, err := newProtocolClient(guard)
	if err != nil {
		return failClose(ProtocolError)
	}
	// No logger or debug writer exists; provider response text stays private.
	if err := cl.Login(c.cfg.Username, c.cfg.Password); err != nil {
		stop()
		_ = conn.Close()
		return nil, err
	}
	if err := cl.identify(); err != nil {
		stop()
		_ = conn.Close()
		return nil, err
	}
	mbox, err := cl.Select(c.cfg.Folder, true)
	if err != nil {
		stop()
		_ = conn.Close()
		return nil, err
	}
	if mbox == nil || !mbox.ReadOnly || mbox.UidValidity == 0 {
		return failClose(ProtocolError)
	}
	return &session{client: cl, conn: conn, stopCancel: stop, info: Info{AccountID: c.cfg.Username, Folder: c.cfg.Folder, UIDValidity: mbox.UidValidity, Messages: mbox.Messages, ReadOnly: true}}, nil
}

func (c *Client) Probe(ctx context.Context) (Info, error) {
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	s, err := c.open(ctx, 1<<20)
	if err != nil {
		return Info{}, err
	}
	defer s.close()
	return s.info, nil
}

func fetchMessages(cl *protocolClient, seqset *imap.SeqSet, items []imap.FetchItem, uid bool, limit int) ([]*imap.Message, error) {
	ch := make(chan *imap.Message, 1)
	done := make(chan error, 1)
	go func() {
		if uid {
			done <- cl.UidFetch(seqset, items, ch)
		} else {
			done <- cl.Fetch(seqset, items, ch)
		}
	}()
	result := make([]*imap.Message, 0, limit)
	invalid := false
	for message := range ch {
		if message == nil || len(result) >= limit {
			invalid = true
			_ = cl.Terminate()
			continue
		}
		result = append(result, message)
	}
	if err := <-done; err != nil || invalid {
		return nil, fail(ProtocolError)
	}
	return result, nil
}

func (c *Client) Fetch(ctx context.Context) (Batch, error) {
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	budget := int64(c.cfg.Limit)*(MaxRawMessageBytes+(16<<10)) + (1 << 20)
	s, err := c.open(ctx, budget)
	if err != nil {
		return Batch{}, err
	}
	defer s.close()
	batch := Batch{Info: s.info, Mails: []Mail{}, Truncated: s.info.Messages > uint32(c.cfg.Limit)}
	if s.info.Messages == 0 {
		return batch, nil
	}
	from := uint32(1)
	if s.info.Messages > uint32(c.cfg.Limit) {
		from = s.info.Messages - uint32(c.cfg.Limit) + 1
	}
	seqset := new(imap.SeqSet)
	seqset.AddRange(from, s.info.Messages)
	metadata, err := fetchMessages(s.client, seqset, []imap.FetchItem{imap.FetchUid, imap.FetchRFC822Size, imap.FetchFlags, imap.FetchInternalDate}, false, c.cfg.Limit)
	if err != nil {
		return Batch{}, err
	}
	sort.Slice(metadata, func(i, j int) bool { return metadata[i].Uid < metadata[j].Uid })
	seenUIDs := map[uint32]bool{}
	for _, meta := range metadata {
		if meta.Uid == 0 || seenUIDs[meta.Uid] || meta.SeqNum < from || meta.SeqNum > s.info.Messages {
			return Batch{}, fail(ProtocolError)
		}
		seenUIDs[meta.Uid] = true
		if meta.Size > MaxRawMessageBytes {
			batch.Skipped++
			continue
		}
		uidset := new(imap.SeqSet)
		uidset.AddNum(meta.Uid)
		section := &imap.BodySectionName{Peek: true}
		messages, err := fetchMessages(s.client, uidset, []imap.FetchItem{imap.FetchUid, section.FetchItem()}, true, 1)
		if err != nil {
			return Batch{}, err
		}
		if len(messages) == 0 {
			batch.Skipped++
			continue
		}
		if messages[0].Uid != meta.Uid {
			return Batch{}, fail(ProtocolError)
		}
		body := messages[0].GetBody(section)
		if body == nil {
			batch.Skipped++
			continue
		}
		raw, err := io.ReadAll(io.LimitReader(body, MaxRawMessageBytes+1))
		if err != nil {
			return Batch{}, fail(ProtocolError)
		}
		if len(raw) > MaxRawMessageBytes {
			batch.Skipped++
			continue
		}
		seen := false
		for _, flag := range meta.Flags {
			if strings.EqualFold(flag, imap.SeenFlag) {
				seen = true
			}
		}
		message, err := parseMail(raw, s.info.UIDValidity, meta.Uid, meta.InternalDate, seen)
		if err != nil {
			batch.Skipped++
			continue
		}
		batch.Mails = append(batch.Mails, message)
	}
	// Sequence-based snapshots can lose entries during remote expunges. Surface
	// any missing records rather than claiming a complete mailbox snapshot.
	expected := int(s.info.Messages)
	if expected > c.cfg.Limit {
		expected = c.cfg.Limit
	}
	if len(metadata) < expected {
		batch.Skipped += expected - len(metadata)
	}
	return batch, nil
}
