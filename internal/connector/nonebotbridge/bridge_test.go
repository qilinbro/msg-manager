package nonebotbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const syntheticSecret = "synthetic-bridge-private-marker-no-platform-io"

type bridgeTestResult struct {
	Value string `json:"value"`
}

var bridgeResponses = map[string]string{
	"success":             `{"version":1,"ok":true,"result":{"value":"isolated"}}`,
	"error":               `{"version":1,"ok":false,"error":{"kind":"permission_denied","retry_after_seconds":2,"platform_code":21001,"send_state":"not_sent"}}`,
	"duplicate":           `{"version":1,"version":1,"ok":true,"result":{"value":"x"}}`,
	"nested-duplicate":    `{"version":1,"ok":true,"result":{"value":"x","value":"y"}}`,
	"case":                `{"Version":1,"ok":true,"result":{"value":"x"}}`,
	"missing-ok":          `{"version":1,"result":{"value":"x"}}`,
	"missing-result":      `{"version":1,"ok":true}`,
	"null-result":         `{"version":1,"ok":true,"result":null}`,
	"error-on-success":    `{"version":1,"ok":true,"result":{"value":"x"},"error":null}`,
	"array-result":        `{"version":1,"ok":true,"result":[]}`,
	"unknown-result":      `{"version":1,"ok":true,"result":{"value":"x","extra":true}}`,
	"trailing":            `{"version":1,"ok":true,"result":{"value":"x"}} {}`,
	"wrong-version":       `{"version":2,"ok":true,"result":{"value":"x"}}`,
	"wrong-bool":          `{"version":1,"ok":1,"result":{"value":"x"}}`,
	"missing-error-field": `{"version":1,"ok":false,"error":{"kind":"auth_failed","platform_code":401,"send_state":"not_sent"}}`,
	"null-error-field":    `{"version":1,"ok":false,"error":{"kind":"auth_failed","retry_after_seconds":null,"platform_code":401,"send_state":"not_sent"}}`,
	"case-error-field":    `{"version":1,"ok":false,"error":{"kind":"auth_failed","Retry_after_seconds":0,"platform_code":401,"send_state":"not_sent"}}`,
	"unknown-kind":        `{"version":1,"ok":false,"error":{"kind":"` + syntheticSecret + `","retry_after_seconds":0,"platform_code":0,"send_state":"not_sent"}}`,
	"bad-state":           `{"version":1,"ok":false,"error":{"kind":"auth_failed","retry_after_seconds":0,"platform_code":401,"send_state":"sent"}}`,
	"bad-retry":           `{"version":1,"ok":false,"error":{"kind":"rate_limited","retry_after_seconds":86401,"platform_code":429,"send_state":"not_sent"}}`,
	"bad-code":            `{"version":1,"ok":false,"error":{"kind":"auth_failed","retry_after_seconds":0,"platform_code":-1,"send_state":"not_sent"}}`,
	"bad-unicode":         `{"version":1,"ok":true,"result":{"value":"\ud800"}}`,
}

// TestBridgeHelperProcess is an isolated fake executable, not a Python/network
// substitute in production. The marker follows -- and contains no credential.
func TestBridgeHelperProcess(t *testing.T) {
	if len(os.Args) < 2 || !strings.HasPrefix(os.Args[len(os.Args)-1], "bridge-helper=") {
		return
	}
	mode := strings.TrimPrefix(os.Args[len(os.Args)-1], "bridge-helper=")
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, MaxRequestBytes+2))
	if err != nil || len(raw) > MaxRequestBytes+1 {
		os.Exit(21)
	}
	var request struct {
		Version int               `json:"version"`
		Action  string            `json:"action"`
		Payload map[string]string `json:"payload"`
	}
	if decodeStrict(raw, &request, MaxRequestBytes+1) != nil || request.Version != 1 || !validAction(request.Action) || request.Payload["secret"] != syntheticSecret {
		os.Exit(22)
	}
	for _, arg := range os.Args {
		if strings.Contains(arg, syntheticSecret) {
			os.Exit(23)
		}
	}
	for _, item := range os.Environ() {
		key := strings.ToUpper(strings.SplitN(item, "=", 2)[0])
		switch key {
		case "SYSTEMROOT", "WINDIR", "TEMP", "TMP", "PATH":
		default:
			os.Exit(24)
		}
	}
	_, _ = fmt.Fprintln(os.Stderr, syntheticSecret) // must never reach a caller
	switch mode {
	case "oversize":
		_, _ = io.WriteString(os.Stdout, strings.Repeat("x", MaxResponseBytes+1024))
	case "hang":
		<-time.After(10 * time.Minute)
	case "exit":
		os.Exit(25)
	case "invalid-utf8":
		_, _ = os.Stdout.Write([]byte{0xff, 0xfe})
	case "deep":
		_, _ = io.WriteString(os.Stdout, `{"version":1,"ok":true,"result":{"value":`+strings.Repeat("[", 40)+`0`+strings.Repeat("]", 40)+`}}`)
	default:
		response, ok := bridgeResponses[mode]
		if !ok {
			os.Exit(26)
		}
		_, _ = io.WriteString(os.Stdout, response+"\n")
	}
	os.Exit(0)
}

func fakeBridge(t *testing.T, mode string) (*Client, **exec.Cmd) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	worker := filepath.Join(t.TempDir(), "worker.py")
	if os.WriteFile(worker, []byte("# synthetic, never executed\n"), 0600) != nil {
		t.Fatal("worker fixture")
	}
	// The helper reuses the Go test binary; -race adds an approximately one-second
	// exit flush. Give completed helpers headroom, not a longer hang deadline or
	// any change to the production timeout or child environment.
	helperTimeout := 5 * time.Second
	if mode == "hang" {
		helperTimeout = time.Second
	}
	cfg := Config{PythonExecutable: binary, WorkerScript: worker, Timeout: helperTimeout}
	client, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var command *exec.Cmd
	client.command = func(ctx context.Context, _ Config) *exec.Cmd {
		command = exec.CommandContext(ctx, binary, "-test.run=^TestBridgeHelperProcess$", "--", "bridge-helper="+mode)
		return command
	}
	return client, &command
}

func TestBridgeSubprocessIsolationAndSuccess(t *testing.T) {
	for _, key := range []string{"SOCIALD_API_TOKEN", "SOCIALD_DATABASE_KEY", "SOCIALD_CREDENTIAL_FILE", "AI_API_KEY", "TELEGRAM_BOT_TOKEN", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "PYTHONPATH", "PYTHONHOME", "HOME", "USERPROFILE"} {
		t.Setenv(key, syntheticSecret)
	}
	client, command := fakeBridge(t, "success")
	var result bridgeTestResult
	if err := client.Call(context.Background(), "telegram.probe", map[string]string{"secret": syntheticSecret}, &result); err != nil || result.Value != "isolated" {
		t.Fatalf("isolated call failed: %v", err)
	}
	if (*command).Dir != filepath.Dir(client.cfg.WorkerScript) || (*command).WaitDelay != 2*time.Second || (*command).Stderr != nil {
		t.Fatal("unbounded working directory, process wait or stderr")
	}
	production := (&Client{cfg: client.cfg, command: func(ctx context.Context, cfg Config) *exec.Cmd {
		return exec.CommandContext(ctx, cfg.PythonExecutable, "-I", "-u", "-B", cfg.WorkerScript)
	}}).command(context.Background(), client.cfg)
	// Inspect the real factory separately, without running a second process.
	original, err := New(client.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original.command(context.Background(), original.cfg).Args, production.Args) {
		t.Fatal("Python isolation flags changed")
	}
}

func TestBridgeErrorEnvelopeIsStructuredAndRedacted(t *testing.T) {
	client, _ := fakeBridge(t, "error")
	var result bridgeTestResult
	err := client.Call(context.Background(), "feishu.send", map[string]string{"secret": syntheticSecret}, &result)
	failure, ok := err.(*Error)
	if !ok || failure.Kind != PermissionDenied || failure.SendState != "not_sent" || failure.RetryAfter != 2*time.Second || failure.PlatformCode != 21001 {
		t.Fatal("lost fixed structured error")
	}
	for _, format := range []string{"%v", "%+v", "%#v"} {
		if strings.Contains(fmt.Sprintf(format, err), syntheticSecret) {
			t.Fatal("error leaked secret")
		}
	}
}

func TestBridgeRejectsMalformedAndOversizeRepliesWithoutReplay(t *testing.T) {
	modes := []string{"duplicate", "nested-duplicate", "case", "missing-ok", "missing-result", "null-result", "error-on-success", "array-result", "unknown-result", "trailing", "wrong-version", "wrong-bool", "missing-error-field", "null-error-field", "case-error-field", "unknown-kind", "bad-state", "bad-retry", "bad-code", "bad-unicode", "invalid-utf8", "deep", "oversize"}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			client, _ := fakeBridge(t, mode)
			starts := 0
			factory := client.command
			client.command = func(ctx context.Context, cfg Config) *exec.Cmd { starts++; return factory(ctx, cfg) }
			var result bridgeTestResult
			err := client.Call(context.Background(), "feishu.send", map[string]string{"secret": syntheticSecret}, &result)
			failure, ok := err.(*Error)
			if !ok || failure.Kind != ProtocolError || failure.SendState != "unknown" || starts != 1 || strings.Contains(err.Error(), syntheticSecret) {
				t.Fatalf("malformed reply not safely bounded: kind=%s, starts=%d", KindOf(err), starts)
			}
		})
	}
}

func TestBridgeFailureAfterStartIsUnknownOnlyForSend(t *testing.T) {
	for _, mode := range []string{"exit", "hang"} {
		for _, action := range []string{"telegram.poll", "feishu.send"} {
			t.Run(mode+"/"+action, func(t *testing.T) {
				client, _ := fakeBridge(t, mode)
				var result bridgeTestResult
				err := client.Call(context.Background(), action, map[string]string{"secret": syntheticSecret}, &result)
				failure, ok := err.(*Error)
				want := "not_sent"
				if action == "feishu.send" {
					want = "unknown"
				}
				if !ok || failure.SendState != want || (failure.Kind != Unavailable && failure.Kind != Canceled) {
					t.Fatal("post-start failure was not conservative")
				}
			})
		}
	}
}

func TestBridgePreflightNeverStartsAWorker(t *testing.T) {
	client, _ := fakeBridge(t, "success")
	client.command = func(context.Context, Config) *exec.Cmd { t.Fatal("preflight started process"); return nil }
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, test := range []struct {
		ctx     context.Context
		action  string
		payload any
		kind    Kind
	}{
		{nil, "feishu.send", nil, InvalidConfig},
		{context.Background(), "generic.call_api", nil, InvalidConfig},
		{context.Background(), "feishu.send", strings.Repeat("x", MaxRequestBytes), InvalidConfig},
		{canceled, "feishu.send", nil, Canceled},
	} {
		var result bridgeTestResult
		err := client.Call(test.ctx, test.action, test.payload, &result)
		if KindOf(err) != test.kind || err.(*Error).SendState != "not_sent" {
			t.Fatal("unsafe preflight classification")
		}
	}
	var zero *Client
	var result bridgeTestResult
	if KindOf(zero.Call(context.Background(), "telegram.poll", nil, &result)) != NotConfigured {
		t.Fatal("nil client was configured")
	}
}

func TestBridgeStartFailureProvesNonDelivery(t *testing.T) {
	client, _ := fakeBridge(t, "success")
	client.command = func(ctx context.Context, _ Config) *exec.Cmd {
		return exec.CommandContext(ctx, filepath.Join(t.TempDir(), "missing.exe"))
	}
	var result bridgeTestResult
	err := client.Call(context.Background(), "feishu.send", map[string]string{"secret": syntheticSecret}, &result)
	if KindOf(err) != Unavailable || err.(*Error).SendState != "not_sent" {
		t.Fatal("failed Start consumed uncertain send")
	}
}

func TestBridgeStrictJSONAndUnicode(t *testing.T) {
	for _, raw := range []string{`{"v":"\ud800"}`, `{"v":"\udfff"}`, `{"v":"\ud800\u0041"}`, `{"v":"\ud800x"}`, `{"v":"\ud800\ud800"}`, `{"x":1,"\u0078":2}`, `{"v":NaN}`, `{"v":1} true`} {
		if validateJSON([]byte(raw), MaxRequestBytes) == nil {
			t.Fatal("accepted ambiguous JSON")
		}
	}
	for _, raw := range []string{`{"v":"\ud83d\ude00"}`, `{"v":"\\ud800"}`, `{"v":"中文😀"}`, `{"v":"\u0000"}`} {
		if validateJSON([]byte(raw), MaxRequestBytes) != nil {
			t.Fatal("rejected valid Unicode JSON")
		}
	}
	if validateJSON([]byte("{}"), 1) == nil {
		t.Fatal("ignored byte bound")
	}
}

func TestBridgeConfigIsLocalPairedBoundedAndRedacted(t *testing.T) {
	client, _ := fakeBridge(t, "success")
	if (Config{}).Validate() != nil {
		t.Fatal("zero config should be disabled")
	}
	invalid := []Config{
		{PythonExecutable: client.cfg.PythonExecutable}, {WorkerScript: client.cfg.WorkerScript},
		{PythonExecutable: "relative.exe", WorkerScript: client.cfg.WorkerScript},
		{PythonExecutable: client.cfg.PythonExecutable, WorkerScript: `\\remote\share\worker.py`},
		{PythonExecutable: client.cfg.PythonExecutable, WorkerScript: client.cfg.WorkerScript + "\n"},
		{PythonExecutable: client.cfg.PythonExecutable, WorkerScript: client.cfg.WorkerScript, Timeout: 61 * time.Second},
		{PythonExecutable: client.cfg.PythonExecutable, WorkerScript: client.cfg.WorkerScript, Timeout: time.Millisecond},
	}
	for _, cfg := range invalid {
		if cfg.Validate() == nil {
			t.Fatal("accepted unsafe runtime")
		}
	}
	missing := client.cfg
	missing.WorkerScript = filepath.Join(t.TempDir(), "missing.py")
	if _, err := New(missing); KindOf(err) != InvalidConfig {
		t.Fatal("accepted missing worker")
	}
	directory := client.cfg
	directory.WorkerScript = filepath.Join(t.TempDir(), "directory.py")
	if os.Mkdir(directory.WorkerScript, 0700) != nil {
		t.Fatal("directory fixture")
	}
	if _, err := New(directory); KindOf(err) != InvalidConfig {
		t.Fatal("accepted directory as script")
	}
	cfg := Config{PythonExecutable: syntheticSecret, WorkerScript: syntheticSecret}
	for _, format := range []string{"%v", "%+v", "%#v"} {
		if strings.Contains(fmt.Sprintf(format, cfg), syntheticSecret) {
			t.Fatal("runtime path leaked")
		}
	}
	raw, err := json.Marshal(cfg)
	if err != nil || strings.Contains(string(raw), syntheticSecret) {
		t.Fatal("runtime config serialized")
	}
}
