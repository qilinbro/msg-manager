// Package nonebotbridge owns the bounded, one-request local Python boundary.
// It has no network listener, shell command, persistent polling or retry path.
package nonebotbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	MaxRequestBytes  = 64 << 10
	MaxResponseBytes = 4 << 20
	DefaultTimeout   = 25 * time.Second
)

type Kind string

const (
	NotConfigured    Kind = "not_configured"
	InvalidConfig    Kind = "invalid_config"
	Unreachable      Kind = "unreachable"
	AuthFailed       Kind = "auth_failed"
	RateLimited      Kind = "rate_limited"
	ProtocolError    Kind = "protocol_error"
	IdentityMismatch Kind = "identity_mismatch"
	WebhookPresent   Kind = "webhook_present"
	Canceled         Kind = "canceled"
	PermissionDenied Kind = "permission_denied"
	PlatformRejected Kind = "platform_rejected"
	SendUnknown      Kind = "send_unknown"
	Unavailable      Kind = "unavailable"
)

type Error struct {
	Kind         Kind
	RetryAfter   time.Duration
	PlatformCode int64
	SendState    string
}

func (e *Error) Error() string {
	if e == nil {
		return "NoneBot bridge: unavailable"
	}
	return "NoneBot bridge: " + string(normalizeKind(e.Kind))
}
func (e *Error) String() string   { return e.Error() }
func (e *Error) GoString() string { return e.Error() }
func KindOf(err error) Kind {
	var failure *Error
	if errors.As(err, &failure) && failure != nil {
		return normalizeKind(failure.Kind)
	}
	return Unavailable
}

// Config is operator-only. No API or model may select a program or script.
// Credentials are intentionally absent and must be supplied through stdin only.
type Config struct {
	PythonExecutable string        `json:"-"`
	WorkerScript     string        `json:"-"`
	Timeout          time.Duration `json:"-"`
}

func (c Config) String() string   { return "nonebotbridge.Config{redacted}" }
func (c Config) GoString() string { return c.String() }
func (c Config) Configured() bool { return c.PythonExecutable != "" && c.WorkerScript != "" }
func (c Config) Validate() error {
	if c.PythonExecutable == "" && c.WorkerScript == "" && c.Timeout == 0 {
		return nil
	}
	if !c.Configured() || !localAbsolute(c.PythonExecutable) || !localAbsolute(c.WorkerScript) || !strings.EqualFold(filepath.Ext(c.WorkerScript), ".py") {
		return &Error{Kind: InvalidConfig, SendState: "not_sent"}
	}
	if runtime.GOOS == "windows" && !strings.EqualFold(filepath.Ext(c.PythonExecutable), ".exe") {
		return &Error{Kind: InvalidConfig, SendState: "not_sent"}
	}
	if c.Timeout != 0 && (c.Timeout < time.Second || c.Timeout > 60*time.Second) {
		return &Error{Kind: InvalidConfig, SendState: "not_sent"}
	}
	return nil
}
func localAbsolute(path string) bool {
	if !filepath.IsAbs(path) || strings.HasPrefix(path, `\\`) || strings.HasPrefix(path, "//") || !utf8.ValidString(path) {
		return false
	}
	for _, char := range path {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

type Caller interface {
	Call(context.Context, string, any, any) error
}

type Client struct {
	cfg Config
	// Injectable only inside this package's tests, never from configuration.
	command func(context.Context, Config) *exec.Cmd
}

func New(cfg Config) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.Configured() {
		for _, path := range []string{cfg.PythonExecutable, cfg.WorkerScript} {
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil || !localAbsolute(resolved) {
				return nil, &Error{Kind: InvalidConfig, SendState: "not_sent"}
			}
			stat, err := os.Stat(resolved)
			if err != nil || !stat.Mode().IsRegular() {
				return nil, &Error{Kind: InvalidConfig, SendState: "not_sent"}
			}
		}
	}
	return &Client{cfg: cfg, command: func(ctx context.Context, cfg Config) *exec.Cmd {
		return exec.CommandContext(ctx, cfg.PythonExecutable, "-I", "-u", "-B", cfg.WorkerScript)
	}}, nil
}
func (c *Client) Configured() bool { return c != nil && c.cfg.Configured() }

type reply struct {
	Version int             `json:"version"`
	OK      *bool           `json:"ok"`
	Result  json.RawMessage `json:"result"`
	Failure *wireError      `json:"error"`
}
type wireError struct {
	Kind              Kind   `json:"kind"`
	RetryAfterSeconds int64  `json:"retry_after_seconds"`
	PlatformCode      int64  `json:"platform_code"`
	SendState         string `json:"send_state"`
}

func (c *Client) Call(ctx context.Context, action string, payload any, result any) error {
	if ctx == nil || !validAction(action) || result == nil {
		return &Error{Kind: InvalidConfig, SendState: "not_sent"}
	}
	if !c.Configured() {
		return &Error{Kind: NotConfigured, SendState: "not_sent"}
	}
	if ctx.Err() != nil {
		return &Error{Kind: Canceled, SendState: "not_sent"}
	}
	request, err := json.Marshal(struct {
		Version int    `json:"version"`
		Action  string `json:"action"`
		Payload any    `json:"payload"`
	}{1, action, payload})
	if err != nil || len(request) > MaxRequestBytes {
		return &Error{Kind: InvalidConfig, SendState: "not_sent"}
	}
	if err := validateJSON(request, MaxRequestBytes); err != nil {
		return &Error{Kind: InvalidConfig, SendState: "not_sent"}
	}
	timeout := c.cfg.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	output := &boundedOutput{max: MaxResponseBytes, cancel: cancel}
	cmd := c.command(callCtx, c.cfg)
	cmd.Env = childEnvironment(c.cfg)
	cmd.Dir = filepath.Dir(c.cfg.WorkerScript)
	cmd.Stdin = bytes.NewReader(append(request, '\n'))
	cmd.Stdout = output
	cmd.Stderr = nil // OS null device: never capture or forward raw Python/platform errors.
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		return &Error{Kind: Unavailable, SendState: "not_sent"}
	}
	state := "not_sent"
	if action == "feishu.send" {
		state = "unknown"
	}
	if err := cmd.Wait(); err != nil {
		if output.exceeded {
			return &Error{Kind: ProtocolError, SendState: state}
		}
		if callCtx.Err() != nil {
			return &Error{Kind: Canceled, SendState: state}
		}
		return &Error{Kind: Unavailable, SendState: state}
	}
	var response reply
	if err := decodeStrict(output.buf.Bytes(), &response, MaxResponseBytes); err != nil || response.Version != 1 || response.OK == nil || !exactEnvelope(output.buf.Bytes(), *response.OK) {
		return &Error{Kind: ProtocolError, SendState: state}
	}
	if !*response.OK {
		failure := response.Failure
		if failure == nil || len(response.Result) != 0 || !validKind(failure.Kind) || failure.RetryAfterSeconds < 0 || failure.RetryAfterSeconds > 86400 || failure.PlatformCode < 0 || failure.PlatformCode > 2147483647 || !validSendState(failure.SendState) {
			return &Error{Kind: ProtocolError, SendState: state}
		}
		return &Error{Kind: failure.Kind, RetryAfter: time.Duration(failure.RetryAfterSeconds) * time.Second, PlatformCode: failure.PlatformCode, SendState: failure.SendState}
	}
	if response.Failure != nil || len(response.Result) == 0 || response.Result[0] != '{' || decodeStrict(response.Result, result, MaxResponseBytes) != nil {
		return &Error{Kind: ProtocolError, SendState: state}
	}
	return nil
}

// encoding/json otherwise matches struct field names case-insensitively and
// accepts null integers as zero. The process contract deliberately does neither.
func exactEnvelope(raw []byte, ok bool) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 3 || fields["version"] == nil || fields["ok"] == nil {
		return false
	}
	if ok {
		return fields["result"] != nil && fields["error"] == nil
	}
	if fields["result"] != nil {
		return false
	}
	var failure map[string]json.RawMessage
	if json.Unmarshal(fields["error"], &failure) != nil || len(failure) != 4 {
		return false
	}
	for _, field := range []string{"kind", "retry_after_seconds", "platform_code", "send_state"} {
		value := bytes.TrimSpace(failure[field])
		if len(value) == 0 || bytes.Equal(value, []byte("null")) {
			return false
		}
	}
	return true
}
func validAction(action string) bool {
	return action == "telegram.probe" || action == "telegram.poll" || action == "feishu.send"
}
func validKind(kind Kind) bool {
	switch kind {
	case NotConfigured, InvalidConfig, Unreachable, AuthFailed, RateLimited, ProtocolError, IdentityMismatch, WebhookPresent, Canceled, PermissionDenied, PlatformRejected, SendUnknown, Unavailable:
		return true
	}
	return false
}
func normalizeKind(kind Kind) Kind {
	if validKind(kind) {
		return kind
	}
	return Unavailable
}
func validSendState(state string) bool {
	return state == "not_sent" || state == "attempted" || state == "responded" || state == "unknown"
}
func childEnvironment(cfg Config) []string {
	// Deliberately omit HOME, credentials, proxy/CA overrides, PYTHONPATH, .env
	// settings and product/model configuration. Python also runs with -I.
	env := []string{}
	for _, name := range []string{"SystemRoot", "WINDIR", "TEMP", "TMP"} {
		if value := os.Getenv(name); value != "" {
			env = append(env, name+"="+value)
		}
	}
	path := filepath.Dir(cfg.PythonExecutable)
	if root := os.Getenv("SystemRoot"); root != "" {
		path += string(os.PathListSeparator) + filepath.Join(root, "System32")
	}
	return append(env, "PATH="+path)
}

type boundedOutput struct {
	buf      bytes.Buffer
	max      int
	exceeded bool
	cancel   context.CancelFunc
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > b.max-b.buf.Len() {
		b.exceeded = true
		b.cancel()
		return 0, errors.New("bounded bridge output exceeded")
	}
	return b.buf.Write(p)
}
func decodeStrict(raw []byte, target any, max int) error {
	if err := validateJSON(raw, max); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}
func validateJSON(raw []byte, max int) error {
	if len(raw) == 0 || len(raw) > max || !utf8.Valid(raw) || !validUnicodeEscapes(raw) {
		return fmt.Errorf("invalid bounded JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := validateValue(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}

// Go's JSON decoder replaces lone UTF-16 surrogate escapes with U+FFFD. Reject
// them instead so the Go and Python sides interpret exactly the same strings.
func validUnicodeEscapes(raw []byte) bool {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		if i+1 >= len(raw) {
			return false
		}
		if raw[i+1] != 'u' {
			i++
			continue
		}
		value, ok := unicodeEscape(raw, i)
		if !ok {
			return false
		}
		i += 5
		if value >= 0xd800 && value <= 0xdbff {
			low, valid := unicodeEscape(raw, i+1)
			if !valid || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		} else if value >= 0xdc00 && value <= 0xdfff {
			return false
		}
	}
	return true
}
func unicodeEscape(raw []byte, start int) (uint16, bool) {
	if start+6 > len(raw) || raw[start] != '\\' || raw[start+1] != 'u' {
		return 0, false
	}
	var value uint16
	for _, digit := range raw[start+2 : start+6] {
		value <<= 4
		switch {
		case digit >= '0' && digit <= '9':
			value += uint16(digit - '0')
		case digit >= 'a' && digit <= 'f':
			value += uint16(digit - 'a' + 10)
		case digit >= 'A' && digit <= 'F':
			value += uint16(digit - 'A' + 10)
		default:
			return 0, false
		}
	}
	return value, true
}
func validateValue(decoder *json.Decoder, depth int) error {
	if depth > 32 {
		return fmt.Errorf("JSON too deep")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, container := token.(json.Delim)
	if !container {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, valid := key.(string)
			if !valid || seen[name] {
				return fmt.Errorf("duplicate JSON key")
			}
			seen[name] = true
			if err := validateValue(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return fmt.Errorf("invalid JSON object")
		}
	case '[':
		for decoder.More() {
			if err := validateValue(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return fmt.Errorf("invalid JSON array")
		}
	default:
		return fmt.Errorf("invalid JSON delimiter")
	}
	return nil
}
