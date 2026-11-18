// Package telegramuser exposes only explicitly requested personal-account reads.
// Authentication material is owned by the local Windows helper, never Go or a model.
package telegramuser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const MaxMessages = 100
const MaxDialogs = 100
const maxOutput = 2 * 1024 * 1024

type Config struct {
	ProjectRoot     string
	PythonPath      string
	ScriptPath      string
	CredentialsPath string
}

func (Config) String() string   { return "Telegram local helper configuration (redacted)" }
func (Config) GoString() string { return "Telegram local helper configuration (redacted)" }

// Resolve derives fixed project-owned paths, without opening credentials or
// contacting Telegram. Only the local service configuration can choose a root.
func (c Config) Resolve() (Config, error) {
	if c.ProjectRoot == "" {
		c.ProjectRoot, _ = os.Getwd()
	}
	if !localAbsolute(c.ProjectRoot) {
		return Config{}, errors.New("invalid Telegram project root")
	}
	if c.PythonPath == "" {
		c.PythonPath = filepath.Join(c.ProjectRoot, ".tools", "telegram-user-venv", "Scripts", "python.exe")
	}
	if c.ScriptPath == "" {
		c.ScriptPath = filepath.Join(c.ProjectRoot, "scripts", "telegram_user", "worker.py")
	}
	if c.CredentialsPath == "" {
		c.CredentialsPath = filepath.Join(c.ProjectRoot, "data", "private", "telegram-user.dpapi")
	}
	for _, path := range []string{c.PythonPath, c.ScriptPath, c.CredentialsPath} {
		if !localAbsolute(path) {
			return Config{}, errors.New("invalid Telegram helper path")
		}
	}
	return c, nil
}

func localAbsolute(path string) bool {
	return filepath.IsAbs(path) && !strings.HasPrefix(path, `\\`) && !strings.ContainsAny(path, "\x00\r\n")
}

type Metadata struct {
	Configured      bool
	HelperAvailable bool
}
type Identity struct {
	AccountID      string `json:"account_id"`
	Name           string `json:"name"`
	ReadOnly       bool   `json:"read_only"`
	SendingEnabled bool   `json:"sending_enabled"`
}
type Dialog struct {
	PeerID string `json:"peer_id"`
	Name   string `json:"name"`
}
type Dialogs struct {
	Identity
	Items   []Dialog `json:"items"`
	Limited bool     `json:"limited"`
}
type Message struct {
	ExternalID string `json:"external_id"`
	Body       string `json:"body"`
	Direction  string `json:"direction"`
	OccurredAt string `json:"occurred_at"`
}
type Batch struct {
	Identity
	PeerID   string    `json:"peer_id"`
	PeerName string    `json:"peer_name"`
	Messages []Message `json:"messages"`
	Skipped  int       `json:"skipped"`
	Limited  bool      `json:"limited"`
}

// IncrementalBatch advances only after its messages have committed locally.
// Cursor also covers unsupported raw records, so media/service messages do not
// stall a text-only subscription forever.
type IncrementalBatch struct {
	Batch
	Cursor string `json:"cursor"`
}
type Error struct {
	Code       string
	RetryAfter int
}

func (e *Error) Error() string { return "Telegram read failed: " + e.Code }

type command struct {
	Action            string  `json:"action"`
	PeerID            string  `json:"peer_id,omitempty"`
	Limit             int     `json:"limit,omitempty"`
	ExpectedAccountID string  `json:"expected_account_id,omitempty"`
	AfterID           *string `json:"after_id,omitempty"`
}
type envelope struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error *struct {
		Code       string `json:"code"`
		RetryAfter int    `json:"retry_after"`
	} `json:"error"`
}

type Client struct {
	cfg Config
	// run is replaced only by package tests. Production always executes the
	// fixed local helper, with a bounded pipe and minimal environment.
	run func(context.Context, []byte) ([]byte, error)
}

func NewClient(cfg Config) (*Client, error) {
	resolved, err := cfg.Resolve()
	if err != nil {
		return nil, err
	}
	c := &Client{cfg: resolved}
	c.run = c.execute
	return c, nil
}

func regular(path string) bool {
	i, err := os.Lstat(path)
	return err == nil && i.Mode().IsRegular() && i.Mode()&os.ModeSymlink == 0
}

// Metadata examines file presence only. It does not decrypt or read a session.
func (c *Client) Metadata() Metadata {
	return Metadata{Configured: regular(c.cfg.CredentialsPath), HelperAvailable: runtime.GOOS == "windows" && regular(c.cfg.PythonPath) && regular(c.cfg.ScriptPath)}
}

func (c *Client) Probe(ctx context.Context) (Identity, error) {
	var v Identity
	err := c.call(ctx, command{Action: "probe"}, &v)
	if err == nil && !ValidIdentity(v) {
		err = &Error{Code: "protocol_error"}
	}
	return v, err
}
func (c *Client) Dialogs(ctx context.Context, expected string) (Dialogs, error) {
	if !ValidID(expected) {
		return Dialogs{}, &Error{Code: "invalid_input"}
	}
	var v Dialogs
	err := c.call(ctx, command{Action: "dialogs", Limit: MaxDialogs, ExpectedAccountID: expected}, &v)
	if err != nil {
		return Dialogs{}, err
	}
	if !ValidIdentity(v.Identity) || v.AccountID != expected || v.Items == nil || len(v.Items) > MaxDialogs {
		return Dialogs{}, &Error{Code: "protocol_error"}
	}
	seen := map[string]bool{}
	for _, d := range v.Items {
		if !ValidID(d.PeerID) || d.PeerID == v.AccountID || !validName(d.Name) || seen[d.PeerID] {
			return Dialogs{}, &Error{Code: "protocol_error"}
		}
		seen[d.PeerID] = true
	}
	return v, nil
}
func (c *Client) Read(ctx context.Context, expected, peer string, limit int) (Batch, error) {
	if !ValidID(expected) || !ValidID(peer) || limit < 1 || limit > MaxMessages {
		return Batch{}, &Error{Code: "invalid_input"}
	}
	var v Batch
	err := c.call(ctx, command{Action: "read", PeerID: peer, Limit: limit, ExpectedAccountID: expected}, &v)
	if err != nil {
		return Batch{}, err
	}
	if v.AccountID != expected || !ValidBatch(v, peer, limit) {
		return Batch{}, &Error{Code: "protocol_error"}
	}
	return v, nil
}

func ValidCursor(value string) bool { return value == "0" || ValidID(value) }

func (c *Client) Baseline(ctx context.Context, expected, peer string) (IncrementalBatch, error) {
	if !ValidID(expected) || !ValidID(peer) {
		return IncrementalBatch{}, &Error{Code: "invalid_input"}
	}
	var v IncrementalBatch
	if err := c.call(ctx, command{Action: "baseline", PeerID: peer, ExpectedAccountID: expected}, &v); err != nil {
		return IncrementalBatch{}, err
	}
	if !ValidBatch(v.Batch, peer, 1) || v.AccountID != expected || len(v.Messages) != 0 || v.Skipped != 0 || v.Limited || !ValidCursor(v.Cursor) {
		return IncrementalBatch{}, &Error{Code: "protocol_error"}
	}
	return v, nil
}

func (c *Client) Scan(ctx context.Context, expected, peer, after string) (IncrementalBatch, error) {
	if !ValidID(expected) || !ValidID(peer) || !ValidCursor(after) {
		return IncrementalBatch{}, &Error{Code: "invalid_input"}
	}
	var v IncrementalBatch
	if err := c.call(ctx, command{Action: "scan", PeerID: peer, ExpectedAccountID: expected, AfterID: &after, Limit: MaxMessages}, &v); err != nil {
		return IncrementalBatch{}, err
	}
	if !ValidIncremental(v, expected, peer, after) {
		return IncrementalBatch{}, &Error{Code: "protocol_error"}
	}
	return v, nil
}

func ValidIncremental(v IncrementalBatch, expected, peer, after string) bool {
	if !ValidBatch(v.Batch, peer, MaxMessages) || v.AccountID != expected || !ValidCursor(after) || !ValidCursor(v.Cursor) {
		return false
	}
	start, _ := strconv.ParseInt(after, 10, 64)
	end, _ := strconv.ParseInt(v.Cursor, 10, 64)
	if end < start || (end == start && (len(v.Messages) > 0 || v.Skipped > 0 || v.Limited)) || (end > start && len(v.Messages)+v.Skipped == 0) {
		return false
	}
	last := start
	for _, m := range v.Messages {
		id, _ := strconv.ParseInt(strings.TrimPrefix(m.ExternalID, "telegram:user:"+peer+":"), 10, 64)
		if id <= last || id > end {
			return false
		}
		last = id
	}
	return true
}

func ValidID(v string) bool {
	n, err := strconv.ParseInt(v, 10, 64)
	return err == nil && n > 0 && strconv.FormatInt(n, 10) == v
}
func validName(v string) bool {
	if !utf8.ValidString(v) || utf8.RuneCountInString(v) > 128 || strings.TrimSpace(v) != v {
		return false
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func ValidIdentity(v Identity) bool {
	return ValidID(v.AccountID) && validName(v.Name) && v.ReadOnly && !v.SendingEnabled
}
func ValidBatch(v Batch, peer string, limit int) bool {
	if !ValidIdentity(v.Identity) || v.PeerID != peer || v.PeerID == v.AccountID || !validName(v.PeerName) || v.Messages == nil || len(v.Messages) > limit || v.Skipped < 0 || v.Skipped > limit || len(v.Messages)+v.Skipped > limit {
		return false
	}
	seen := map[string]bool{}
	for _, m := range v.Messages {
		prefix := "telegram:user:" + peer + ":"
		id := strings.TrimPrefix(m.ExternalID, prefix)
		at, err := time.Parse(time.RFC3339Nano, m.OccurredAt)
		if !strings.HasPrefix(m.ExternalID, prefix) || !ValidID(id) || seen[m.ExternalID] || len(m.Body) < 1 || len(m.Body) > 16000 || !utf8.ValidString(m.Body) || strings.ContainsRune(m.Body, 0) || (m.Direction != "incoming" && m.Direction != "outgoing") || err != nil || at.Before(time.Unix(0, 0)) || at.After(time.Now().Add(24*time.Hour)) {
			return false
		}
		seen[m.ExternalID] = true
	}
	return true
}

func safeCode(code string) string {
	switch code {
	case "NOT_CONFIGURED":
		return "requires_initialization"
	case "CONFIG_INVALID", "CONFIG_UNAVAILABLE", "CONFIG_UNSAFE", "CONFIG_MISSING":
		return "config_invalid"
	case "WINDOWS_REQUIRED":
		return "unsupported_platform"
	case "INVALID_INPUT":
		return "invalid_input"
	case "DEPENDENCY_MISSING":
		return "helper_unavailable"
	case "AUTH_REQUIRED":
		return "logged_out"
	case "IDENTITY_MISMATCH":
		return "identity_mismatch"
	case "PEER_NOT_AVAILABLE":
		return "peer_not_found"
	case "FLOOD_WAIT":
		return "flood_wait"
	case "API_INVALID", "LOGIN_INVALID":
		return "auth_failed"
	case "UNREACHABLE":
		return "network_error"
	}
	switch code {
	case "not_configured", "requires_initialization", "helper_unavailable", "unsupported_platform", "invalid_input", "auth_failed", "logged_out", "identity_mismatch", "flood_wait", "network_error", "timeout", "peer_not_found", "protocol_error", "busy", "config_invalid", "password_required":
		return code
	default:
		return "unavailable"
	}
}

func (c *Client) call(ctx context.Context, request command, target any) error {
	if ctx == nil {
		return &Error{Code: "invalid_input"}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	input, _ := json.Marshal(request)
	output, err := c.run(ctx, input)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var failure *Error
		if errors.As(err, &failure) {
			return &Error{Code: safeCode(failure.Code)}
		}
		return &Error{Code: "unavailable"}
	}
	if len(output) > maxOutput {
		return &Error{Code: "protocol_error"}
	}
	var result envelope
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || decoder.Decode(new(any)) != io.EOF {
		return &Error{Code: "protocol_error"}
	}
	if !result.OK {
		if result.Error == nil {
			return &Error{Code: "protocol_error"}
		}
		retry := result.Error.RetryAfter
		if retry < 0 || retry > 7*86400 {
			retry = 0
		}
		return &Error{Code: safeCode(result.Error.Code), RetryAfter: retry}
	}
	if result.Error != nil || len(result.Data) == 0 {
		return &Error{Code: "protocol_error"}
	}
	decoder = json.NewDecoder(bytes.NewReader(result.Data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(new(any)) != io.EOF {
		return &Error{Code: "protocol_error"}
	}
	return nil
}

type boundedOutput struct{ buffer bytes.Buffer }

func (w *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > maxOutput-w.buffer.Len() {
		return 0, errors.New("Telegram helper output exceeded bound")
	}
	return w.buffer.Write(p)
}

func (c *Client) execute(ctx context.Context, input []byte) ([]byte, error) {
	if runtime.GOOS != "windows" {
		return nil, &Error{Code: "unsupported_platform"}
	}
	meta := c.Metadata()
	if !meta.HelperAvailable {
		return nil, &Error{Code: "helper_unavailable"}
	}
	if !meta.Configured {
		return nil, &Error{Code: "requires_initialization"}
	}
	cmd := exec.CommandContext(ctx, c.cfg.PythonPath, "-I", c.cfg.ScriptPath)
	cmd.Dir = c.cfg.ProjectRoot
	cmd.Env = helperEnv()
	cmd.Stdin = bytes.NewReader(input)
	var output boundedOutput
	cmd.Stdout = &output
	cmd.Stderr = io.Discard
	hideWindow(cmd)
	// WaitDelay bounds inherited pipes even if a malformed helper spawns a child.
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return output.buffer.Bytes(), nil
}

func helperEnv() []string {
	result := []string{"PYTHONIOENCODING=utf-8", "PYTHONUTF8=1"}
	for _, key := range []string{"SystemRoot", "WINDIR", "TEMP", "TMP", "USERPROFILE", "LOCALAPPDATA"} {
		if value := os.Getenv(key); value != "" {
			result = append(result, key+"="+value)
		}
	}
	return result
}
