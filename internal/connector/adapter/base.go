package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const maxResponseBytes = 1 << 20

// Endpoint describes a locally configured protocol provider. All values come
// from the operator's environment; request bodies never select a target.
type Endpoint struct {
	URL            string
	AccessToken    string
	ExpectedSelfID string
	Timeout        time.Duration
}

func (Endpoint) String() string   { return "protocol endpoint (redacted)" }
func (Endpoint) GoString() string { return "protocol endpoint (redacted)" }

// Base is the shared transport boundary. Protocol packages embed it and add
// only their own request/response shapes; none can weaken these checks.
type Base struct {
	apiBase  *url.URL
	eventURL *url.URL
	token    string
	selfID   string
	dialHost string
	port     string
	timeout  time.Duration
}

func New(e Endpoint) (Base, error) {
	if e.URL == "" || e.AccessToken == "" || e.ExpectedSelfID == "" {
		return Base{}, Failure(NotConfigured)
	}
	if len(e.URL) > 4096 || len(e.AccessToken) > 4096 {
		return Base{}, Failure(InvalidConfig)
	}
	for _, ch := range e.AccessToken {
		if ch < 33 || ch > 126 {
			return Base{}, Failure(InvalidConfig)
		}
	}
	selfID, ok := ExactID(json.RawMessage(strconv.Quote(e.ExpectedSelfID)))
	if !ok {
		return Base{}, Failure(InvalidConfig)
	}
	u, err := url.Parse(e.URL)
	if err != nil || u == nil || u.Scheme != "http" || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(e.URL, "#\\") {
		return Base{}, Failure(InvalidConfig)
	}
	if u.Path != "" && u.Path != "/" {
		return Base{}, Failure(InvalidConfig)
	}
	host := u.Hostname()
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return Base{}, Failure(InvalidConfig)
	}
	port := u.Port()
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
		return Base{}, Failure(InvalidConfig)
	}
	if u.Host != net.JoinHostPort(host, port) {
		return Base{}, Failure(InvalidConfig)
	}
	if host == "localhost" {
		host = "127.0.0.1" // Never resolve a configurable hostname using DNS.
	}
	timeout := e.Timeout
	if timeout == 0 {
		timeout = 8 * time.Second
	}
	if timeout < time.Second || timeout > 30*time.Second {
		return Base{}, Failure(InvalidConfig)
	}
	apiURL := *u
	apiURL.Path = ""
	apiURL.RawPath = ""
	eventURL := url.URL{Scheme: "ws", Host: net.JoinHostPort(host, port), Path: "/event"}
	return Base{apiBase: &apiURL, eventURL: &eventURL, token: e.AccessToken, selfID: selfID, dialHost: host, port: port, timeout: timeout}, nil
}

func (b Base) SelfID() string                 { return b.selfID }
func (b Base) Timeout() time.Duration         { return b.timeout }
func (b Base) Decode(raw []byte, v any) error { return DecodeJSON(raw, v) }

// Dial pins every connection to the validated literal address. No DNS, proxy
// or redirect can change the destination.
func (b Base) Dial(ctx context.Context, _, _ string) (net.Conn, error) {
	d := net.Dialer{Timeout: b.timeout}
	return d.DialContext(ctx, "tcp", net.JoinHostPort(b.dialHost, b.port))
}

// Call performs one Milky-style POST /api/{action}. Business failures are
// returned as stable classifications; provider text is never surfaced.
func (b Base) Call(ctx context.Context, action string, params any) (json.RawMessage, error) {
	if b.apiBase == nil {
		return nil, Failure(NotConfigured)
	}
	if !validAction(action) {
		return nil, Failure(ProtocolError)
	}
	body := []byte("{}")
	if params != nil {
		encoded, err := json.Marshal(params)
		if err != nil {
			return nil, Failure(ProtocolError)
		}
		body = encoded
	}
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	u := *b.apiBase
	u.Path = "/api/" + action
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, Failure(InvalidConfig)
	}
	req.Header.Set("Authorization", "Bearer "+b.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	transport := &http.Transport{DialContext: b.Dial, Proxy: nil, DisableKeepAlives: true, MaxResponseHeaderBytes: 32 << 10, ResponseHeaderTimeout: b.timeout}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: b.timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return nil, Failure(Unreachable)
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, Failure(AuthFailed)
	case http.StatusOK:
	default:
		return nil, Failure(ProtocolError)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, Failure(Unreachable)
	}
	return decodeEnvelope(raw)
}

// DialEvent opens the forward WebSocket /event stream. The product always
// dials out; inbound WebHooks are not accepted.
func (b Base) DialEvent(ctx context.Context) (*websocket.Conn, error) {
	if b.eventURL == nil {
		return nil, Failure(NotConfigured)
	}
	dialer := websocket.Dialer{NetDialContext: b.Dial, HandshakeTimeout: b.timeout, Proxy: nil}
	header := http.Header{"Authorization": []string{"Bearer " + b.token}}
	conn, response, err := dialer.DialContext(ctx, b.eventURL.String(), header)
	if err != nil {
		if response != nil {
			if response.Body != nil {
				_ = response.Body.Close()
			}
			if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
				return nil, Failure(AuthFailed)
			}
		}
		return nil, Failure(Unreachable)
	}
	conn.SetReadLimit(maxResponseBytes)
	return conn, nil
}

func decodeEnvelope(raw []byte) (json.RawMessage, error) {
	var envelope struct {
		Status  string          `json:"status"`
		Retcode *int64          `json:"retcode"`
		Data    json.RawMessage `json:"data"`
	}
	if err := DecodeJSON(raw, &envelope); err != nil {
		return nil, Failure(ProtocolError)
	}
	if envelope.Retcode == nil {
		return nil, Failure(ProtocolError)
	}
	if *envelope.Retcode == -403 {
		return nil, Failure(LoggedOut)
	}
	if envelope.Status != "ok" || *envelope.Retcode != 0 || len(envelope.Data) == 0 || bytes.Equal(bytes.TrimSpace(envelope.Data), []byte("null")) {
		return nil, Failure(ProtocolError)
	}
	return envelope.Data, nil
}

func validAction(action string) bool {
	if action == "" || len(action) > 64 {
		return false
	}
	for _, ch := range action {
		if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '_') {
			return false
		}
	}
	return true
}
