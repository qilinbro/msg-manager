package onebot

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const probeTimeout = 8 * time.Second
const maxSkippedFrames = 32

type LoginInfo struct {
	UserID   string `json:"user_id"`
	Nickname string `json:"nickname"`
	Online   bool   `json:"online"`
}

type Client struct {
	config validatedConfig
	// Unexported to prevent production callers from weakening boundary checks.
	timeout time.Duration
}

func NewClient(cfg Config) (*Client, error) {
	validated, err := validateConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &Client{config: validated, timeout: probeTimeout}, nil
}

// Probe performs two read-only actions. Success verifies the configured account
// and online/good status, not receiving reliability or permission to send.
func (c *Client) Probe(ctx context.Context) (LoginInfo, error) {
	if c == nil || c.config.url == nil {
		return LoginInfo{}, failure(NotConfigured)
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	var call func(string) (json.RawMessage, error)
	if c.config.url.Scheme == "ws" {
		conn, err := c.openWebSocket(ctx)
		if err != nil {
			return LoginInfo{}, err
		}
		defer conn.Close()
		stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
		defer stop()
		call = func(action string) (json.RawMessage, error) { return c.callWebSocket(ctx, conn, action) }
	} else {
		transport := &http.Transport{DialContext: c.dialContext, Proxy: nil, DisableKeepAlives: true, MaxResponseHeaderBytes: 32 << 10, ResponseHeaderTimeout: c.timeout}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: c.timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		call = func(action string) (json.RawMessage, error) { return c.callHTTP(ctx, client, action) }
	}
	loginData, err := call("get_login_info")
	if err != nil {
		return LoginInfo{}, err
	}
	var login struct {
		UserID   json.RawMessage `json:"user_id"`
		Nickname *string         `json:"nickname"`
	}
	if decodeJSON(loginData, &login) != nil || login.Nickname == nil {
		return LoginInfo{}, failure(ProtocolError)
	}
	selfID, ok := exactID(login.UserID, false)
	if !ok {
		return LoginInfo{}, failure(ProtocolError)
	}
	if selfID != c.config.selfID {
		return LoginInfo{}, failure(IdentityMismatch)
	}
	statusData, err := call("get_status")
	if err != nil {
		return LoginInfo{}, err
	}
	var status struct {
		Online *bool `json:"online"`
		Good   *bool `json:"good"`
	}
	if decodeJSON(statusData, &status) != nil || status.Online == nil || status.Good == nil {
		return LoginInfo{}, failure(ProtocolError)
	}
	if !*status.Online || !*status.Good {
		return LoginInfo{}, failure(LoggedOut)
	}
	return LoginInfo{UserID: selfID, Nickname: *login.Nickname, Online: true}, nil
}

func (c *Client) dialContext(ctx context.Context, network, _ string) (net.Conn, error) {
	dialer := net.Dialer{Timeout: c.timeout}
	return dialer.DialContext(ctx, "tcp", net.JoinHostPort(c.config.dialHost, c.config.port))
}

func (c *Client) callHTTP(ctx context.Context, client *http.Client, action string) (json.RawMessage, error) {
	u := *c.config.url
	u.Path = strings.TrimRight(u.Path, "/") + "/" + action
	u.RawPath = ""
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewBufferString("{}"))
	if err != nil {
		return nil, failure(InvalidConfig)
	}
	req.Header.Set("Authorization", "Bearer "+c.config.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return nil, failure(Unreachable)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return nil, failure(AuthFailed)
	}
	if response.StatusCode != http.StatusOK {
		return nil, failure(ProtocolError)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, failure(Unreachable)
	}
	return decodeResponse(raw)
}

func (c *Client) openWebSocket(ctx context.Context) (*websocket.Conn, error) {
	dialer := websocket.Dialer{NetDialContext: c.dialContext, HandshakeTimeout: c.timeout, Proxy: nil}
	header := http.Header{"Authorization": []string{"Bearer " + c.config.token}}
	conn, response, err := dialer.DialContext(ctx, c.config.url.String(), header)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if response != nil && (response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden) {
			return nil, failure(AuthFailed)
		}
		return nil, failure(Unreachable)
	}
	conn.SetReadLimit(maxResponseBytes)
	return conn, nil
}

func (c *Client) callWebSocket(ctx context.Context, conn *websocket.Conn, action string) (json.RawMessage, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, failure(Unreachable)
	}
	echo := hex.EncodeToString(random[:])
	deadline, _ := ctx.Deadline()
	if err := conn.SetWriteDeadline(deadline); err != nil {
		return nil, failure(Unreachable)
	}
	if err := conn.SetReadDeadline(deadline); err != nil {
		return nil, failure(Unreachable)
	}
	if err := conn.WriteJSON(struct {
		Action string         `json:"action"`
		Params map[string]any `json:"params"`
		Echo   string         `json:"echo"`
	}{Action: action, Params: map[string]any{}, Echo: echo}); err != nil {
		return nil, failure(Unreachable)
	}
	for skipped := 0; skipped <= maxSkippedFrames; skipped++ {
		kind, raw, err := conn.ReadMessage()
		if err != nil {
			if errors.Is(err, websocket.ErrReadLimit) {
				return nil, failure(ProtocolError)
			}
			return nil, failure(Unreachable)
		}
		if kind != websocket.TextMessage {
			return nil, failure(ProtocolError)
		}
		var candidate struct {
			Echo json.RawMessage `json:"echo"`
		}
		if decodeJSON(raw, &candidate) != nil {
			return nil, failure(ProtocolError)
		}
		var responseEcho string
		if json.Unmarshal(candidate.Echo, &responseEcho) != nil || responseEcho != echo {
			continue // Events or another request's receipt cannot verify this call.
		}
		return decodeResponse(raw)
	}
	return nil, failure(ProtocolError)
}

func decodeResponse(raw []byte) (json.RawMessage, error) {
	var response struct {
		Status  string          `json:"status"`
		Retcode *int64          `json:"retcode"`
		Data    json.RawMessage `json:"data"`
	}
	if decodeJSON(raw, &response) != nil || response.Retcode == nil {
		return nil, failure(ProtocolError)
	}
	if *response.Retcode == 1401 || *response.Retcode == 1403 {
		return nil, failure(AuthFailed)
	}
	if response.Status != "ok" || *response.Retcode != 0 || len(response.Data) == 0 || bytes.Equal(bytes.TrimSpace(response.Data), []byte("null")) {
		return nil, failure(ProtocolError)
	}
	return response.Data, nil
}
