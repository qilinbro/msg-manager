// Package feishu implements the fixed, send-only Feishu application-bot API.
// It never discovers recipients, reads chats, follows redirects, or retries a
// request. Callers must persist authorization, a frozen payload, and an outbox
// claim before calling SendText.
package feishu

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	tokenURL        = "https://accounts.feishu.cn/oauth/v3/token"
	messageURL      = "https://open.feishu.cn/open-apis/im/v1/messages?receive_id_type=open_id"
	sendScope       = "im:message:send_as_bot"
	MaxRequestBytes = 16 * 1024
	MaxTextBytes    = 12000
	maxResponseSize = 64 * 1024
	maxTokenSize    = 16 * 1024

	CodeInvalidInput         = "FEISHU_INVALID_INPUT"
	CodeMessageTooLarge      = "FEISHU_MESSAGE_TOO_LARGE"
	CodeAuthFailed           = "FEISHU_AUTH_FAILED"
	CodeAuthUnavailable      = "FEISHU_AUTH_UNAVAILABLE"
	CodePermissionDenied     = "FEISHU_PERMISSION_DENIED"
	CodeRateLimited          = "FEISHU_RATE_LIMITED"
	CodeQuotaExceeded        = "FEISHU_QUOTA_EXCEEDED"
	CodeRecipientUnavailable = "FEISHU_RECIPIENT_UNAVAILABLE"
	CodeRejected             = "FEISHU_REJECTED"
	CodeSendUnknown          = "FEISHU_SEND_UNKNOWN"
	CodeSendPending          = "FEISHU_SEND_PENDING"
	CodeClosed               = "FEISHU_CLIENT_CLOSED"
)

// Credentials are backend-only. Diagnostic and JSON formatting are deliberately
// redacted; request serialization below selects the individual fields explicitly.
type Credentials struct {
	AppID     string `json:"-"`
	AppSecret string `json:"-"`
}

func (Credentials) String() string               { return "feishu.Credentials{[redacted]}" }
func (credentials Credentials) GoString() string { return credentials.String() }
func (Credentials) MarshalJSON() ([]byte, error) { return []byte(`"[redacted]"`), nil }

// SendInput must come from a durable, target-bound report, never model arguments.
// UUID is persisted by the caller. Feishu only deduplicates this value for one
// hour; the caller's durable ledger remains responsible for long-term deduplication.
type SendInput struct {
	RecipientOpenID string `json:"-"`
	Text            string `json:"-"`
	UUID            string `json:"-"`
}

func (SendInput) String() string               { return "feishu.SendInput{[redacted]}" }
func (input SendInput) GoString() string       { return input.String() }
func (SendInput) MarshalJSON() ([]byte, error) { return []byte(`"[redacted]"`), nil }

type Receipt struct {
	MessageID string `json:"message_id"`
}

type Sender interface {
	SendText(context.Context, Credentials, SendInput) (Receipt, error)
}

// Error contains only a fixed diagnostic code. Unknown means the message may
// have been accepted and must not be replayed, including after a process restart.
// A token-stage error is not an uncertain message delivery: no message was sent.
type Error struct {
	Code    string
	Unknown bool
}

func (failure *Error) Error() string {
	if failure != nil {
		switch failure.Code {
		case CodeInvalidInput, CodeMessageTooLarge, CodeAuthFailed, CodeAuthUnavailable,
			CodePermissionDenied, CodeRateLimited, CodeQuotaExceeded, CodeRecipientUnavailable,
			CodeRejected, CodeSendUnknown, CodeSendPending, CodeClosed:
			return failure.Code
		}
	}
	return CodeSendUnknown
}

func fail(code string, unknown bool) error { return &Error{Code: code, Unknown: unknown} }

// Client has a single bounded in-memory token cache. Production endpoints and
// transport policy are not configurable through environment variables or APIs.
type Client struct {
	http  *http.Client
	now   func() time.Time
	mu    sync.Mutex
	cache struct {
		key       [32]byte
		token     string
		refreshAt time.Time
	}
	closed bool
}

func (*Client) String() string               { return "feishu.Client{[redacted]}" }
func (client *Client) GoString() string      { return client.String() }
func (*Client) MarshalJSON() ([]byte, error) { return []byte(`"[redacted]"`), nil }

func NewClient() *Client {
	// HTTP/1.1, no connection reuse, and a nil GetBody also avoid implicit
	// transport replays of POSTs. No SDK or automatic token/send retry is used.
	transport := &http.Transport{
		Proxy:                  nil,
		DialContext:            (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
		TLSNextProto:           map[string]func(string, *tls.Conn) http.RoundTripper{},
		ForceAttemptHTTP2:      false,
		DisableKeepAlives:      true,
		TLSHandshakeTimeout:    5 * time.Second,
		ResponseHeaderTimeout:  10 * time.Second,
		MaxResponseHeaderBytes: 32 * 1024,
	}
	return &Client{
		http: &http.Client{
			Transport: transport,
			Timeout:   20 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return fail(CodeSendUnknown, true)
			},
		},
		now: time.Now,
	}
}

// Close drops the cached token and closes idle connections; callers must cancel
// and drain their own in-flight request contexts before closing the service.
func (client *Client) Close() error {
	client.mu.Lock()
	client.closed = true
	client.cache.token = ""
	client.cache.key = [32]byte{}
	client.cache.refreshAt = time.Time{}
	client.mu.Unlock()
	client.http.CloseIdleConnections()
	return nil
}

func asciiIdentifier(value, prefix string, max int) bool {
	if !strings.HasPrefix(value, prefix) || len(value) <= len(prefix) || len(value) > max {
		return false
	}
	for _, ch := range value[len(prefix):] {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-') {
			return false
		}
	}
	return true
}

func headerSafe(value string, max int) bool {
	if value == "" || len(value) > max {
		return false
	}
	for _, ch := range value {
		if ch < 0x21 || ch > 0x7e {
			return false
		}
	}
	return true
}

// ValidateInput checks the exact two-layer JSON request budget without making
// any request. It is also suitable for validating a frozen report before saving.
func ValidateInput(credentials Credentials, input SendInput) error {
	_, err := encodeInput(credentials, input)
	return err
}

func encodeInput(credentials Credentials, input SendInput) ([]byte, error) {
	if !asciiIdentifier(credentials.AppID, "cli_", 128) || !headerSafe(credentials.AppSecret, 4096) ||
		!asciiIdentifier(input.RecipientOpenID, "ou_", 128) || !asciiIdentifier(input.UUID, "", 50) ||
		!utf8.ValidString(input.Text) || strings.TrimSpace(input.Text) == "" || strings.Contains(input.Text, credentials.AppSecret) {
		return nil, fail(CodeInvalidInput, false)
	}
	if len(input.Text) > MaxTextBytes {
		return nil, fail(CodeMessageTooLarge, false)
	}
	for _, ch := range input.Text {
		if ch < 0x20 && ch != '\n' && ch != '\t' || ch == 0x7f {
			return nil, fail(CodeInvalidInput, false)
		}
	}
	content, _ := json.Marshal(struct {
		Text string `json:"text"`
	}{Text: input.Text})
	body, _ := json.Marshal(struct {
		ReceiveID string `json:"receive_id"`
		MsgType   string `json:"msg_type"`
		Content   string `json:"content"`
		UUID      string `json:"uuid"`
	}{ReceiveID: input.RecipientOpenID, MsgType: "text", Content: string(content), UUID: input.UUID})
	if len(body) > MaxRequestBytes {
		return nil, fail(CodeMessageTooLarge, false)
	}
	return body, nil
}

func credentialKey(credentials Credentials) [32]byte {
	return sha256.Sum256([]byte(credentials.AppID + "\x00" + credentials.AppSecret))
}

func (client *Client) SendText(ctx context.Context, credentials Credentials, input SendInput) (Receipt, error) {
	body, err := encodeInput(credentials, input)
	if err != nil {
		return Receipt{}, err
	}
	if ctx == nil || ctx.Err() != nil {
		return Receipt{}, fail(CodeInvalidInput, false)
	}
	token, err := client.accessToken(ctx, credentials)
	if err != nil {
		return Receipt{}, err
	}
	if ctx.Err() != nil || strings.Contains(input.Text, token) {
		return Receipt{}, fail(CodeInvalidInput, false)
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, messageURL, bytes.NewReader(body))
	request.GetBody = nil
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	request.Header.Set("Accept", "application/json")
	response, err := client.http.Do(request)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return Receipt{}, fail(CodeSendUnknown, true)
	}
	defer response.Body.Close()
	// Even a business-code rejection cannot prove non-delivery after HTTP 5xx.
	if response.StatusCode >= 500 {
		return Receipt{}, fail(CodeSendUnknown, true)
	}
	fields, ok := responseFields(response)
	if !ok {
		return Receipt{}, fail(CodeSendUnknown, true)
	}
	code, present := integerField(fields, "code")
	if !present {
		return Receipt{}, fail(CodeSendUnknown, true)
	}
	if code != 0 {
		if data, valid := objectFields(fields["data"]); valid {
			var contradictoryID string
			if json.Unmarshal(data["message_id"], &contradictoryID) == nil && contradictoryID != "" {
				return Receipt{}, fail(CodeSendUnknown, true)
			}
		}
		errorCode, unknown := messageFailure(code)
		if errorCode == CodeAuthFailed {
			client.invalidate(credentials, token)
		}
		return Receipt{}, fail(errorCode, unknown)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Receipt{}, fail(CodeSendUnknown, true)
	}
	data, ok := objectFields(fields["data"])
	if !ok {
		return Receipt{}, fail(CodeSendUnknown, true)
	}
	var messageID string
	if json.Unmarshal(data["message_id"], &messageID) != nil || !headerSafe(messageID, 256) ||
		strings.Contains(messageID, credentials.AppSecret) || strings.Contains(messageID, token) || strings.Contains(messageID, input.RecipientOpenID) {
		return Receipt{}, fail(CodeSendUnknown, true)
	}
	return Receipt{MessageID: messageID}, nil
}

func (client *Client) accessToken(ctx context.Context, credentials Credentials) (string, error) {
	// This one-entry critical section coalesces token acquisition, including
	// refreshes. Message submission itself is never held under this mutex.
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closed {
		return "", fail(CodeClosed, false)
	}
	if ctx.Err() != nil {
		return "", fail(CodeAuthUnavailable, false)
	}
	key := credentialKey(credentials)
	if client.cache.key == key && client.cache.token != "" && client.now().Before(client.cache.refreshAt) {
		return client.cache.token, nil
	}
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {credentials.AppID},
		"client_secret": {credentials.AppSecret},
		"scope":         {sendScope},
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	request.GetBody = nil
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	startedAt := client.now()
	response, err := client.http.Do(request)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return "", fail(CodeAuthUnavailable, false)
	}
	defer response.Body.Close()
	fields, ok := responseFields(response)
	if !ok || response.StatusCode >= 500 {
		return "", fail(CodeAuthUnavailable, false)
	}
	code, present := integerField(fields, "code")
	if !present {
		return "", fail(CodeAuthUnavailable, false)
	}
	if code != 0 {
		switch code {
		case 20002, 20009, 20048, 20069, 20130, 20140:
			return "", fail(CodeAuthFailed, false)
		case 20068:
			return "", fail(CodePermissionDenied, false)
		case 99991400:
			return "", fail(CodeRateLimited, false)
		case 99991403:
			return "", fail(CodeQuotaExceeded, false)
		default:
			return "", fail(CodeAuthUnavailable, false)
		}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fail(CodeAuthUnavailable, false)
	}
	var token, tokenType, scope string
	expires, validExpiry := integerField(fields, "expires_in")
	if json.Unmarshal(fields["access_token"], &token) != nil || !headerSafe(token, maxTokenSize) ||
		json.Unmarshal(fields["token_type"], &tokenType) != nil || !strings.EqualFold(tokenType, "Bearer") ||
		!validExpiry || expires < 1 || expires > 86400 {
		return "", fail(CodeAuthUnavailable, false)
	}
	if json.Unmarshal(fields["scope"], &scope) != nil || strings.Join(strings.Fields(scope), " ") != sendScope {
		return "", fail(CodePermissionDenied, false)
	}
	lifetime := time.Duration(expires) * time.Second
	margin := lifetime / 10
	if margin > time.Minute {
		margin = time.Minute
	}
	refreshAt := startedAt.Add(lifetime - margin)
	if !client.now().Before(refreshAt) {
		return "", fail(CodeAuthUnavailable, false)
	}
	client.cache.key, client.cache.token, client.cache.refreshAt = key, token, refreshAt
	return token, nil
}

func (client *Client) invalidate(credentials Credentials, token string) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.cache.key == credentialKey(credentials) && client.cache.token == token {
		client.cache.token = ""
		client.cache.refreshAt = time.Time{}
	}
}

func messageFailure(code int64) (string, bool) {
	switch code {
	case 230049:
		return CodeSendPending, true
	case 230001, 230022, 230025, 230028, 230075:
		return CodeRejected, false
	case 230006, 99991672, 99991676:
		return CodePermissionDenied, false
	case 230013, 230029, 230034, 230035, 230053:
		return CodeRecipientUnavailable, false
	case 230020, 99991400:
		return CodeRateLimited, false
	case 99991403:
		return CodeQuotaExceeded, false
	case 99991663, 99991665:
		return CodeAuthFailed, false
	default:
		return CodeSendUnknown, true
	}
}

func responseFields(response *http.Response) (map[string]json.RawMessage, bool) {
	kind, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || kind != "application/json" || response.ContentLength > maxResponseSize {
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseSize+1))
	if err != nil || len(body) > maxResponseSize {
		return nil, false
	}
	return objectFields(body)
}

// Only exact, unambiguous keys may decide success. Additional official response
// fields are allowed, but duplicate keys, null/missing codes and trailing JSON
// can never be mistaken for a successful platform receipt.
func objectFields(body []byte) (map[string]json.RawMessage, bool) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return nil, false
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok {
			return nil, false
		}
		if _, exists := fields[name]; exists {
			return nil, false
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return nil, false
		}
		fields[name] = value
	}
	if last, err := decoder.Token(); err != nil || last != json.Delim('}') {
		return nil, false
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return nil, false
	}
	return fields, true
}

func integerField(fields map[string]json.RawMessage, name string) (int64, bool) {
	raw, exists := fields[name]
	if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return 0, false
	}
	var value int64
	if json.Unmarshal(raw, &value) != nil {
		return 0, false
	}
	return value, true
}
