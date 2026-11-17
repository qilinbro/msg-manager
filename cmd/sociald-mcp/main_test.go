package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	processTestFlag        = "SOCIALD_MCP_TEST_PROCESS"
	processTestToken       = "synthetic-process-token-12345678901234567890"
	processProtocolVersion = "2025-11-25"
)

// Run the real entry point in a separate process. Explicit os.Exit prevents the
// testing package from adding PASS to the server's JSON-RPC stdout stream.
func TestMCPHelperProcess(t *testing.T) {
	if os.Getenv(processTestFlag) != "1" {
		return
	}
	main()
	os.Exit(0)
}

func processTestContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func processCommand(t *testing.T, ctx context.Context, baseURL, token string) *exec.Cmd {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestMCPHelperProcess$")
	cmd.WaitDelay = 2 * time.Second
	// Do not inherit the user's service credentials or other configuration.
	cmd.Env = []string{
		processTestFlag + "=1",
		"SOCIALD_URL=" + baseURL,
		"SOCIALD_API_TOKEN=" + token,
		// A stripped child environment alone does not isolate Windows Known
		// Folder APIs. Always pin a synthetic path, even in env-only tests.
		"SOCIALD_CREDENTIAL_FILE=" + filepath.Join(t.TempDir(), "missing-synthetic.dpapi"),
	}
	return cmd
}

type processClient struct {
	cmd     *exec.Cmd
	session *sdk.ClientSession
	stderr  bytes.Buffer
}

func processSDKClient(t *testing.T, ctx context.Context, baseURL string) *processClient {
	t.Helper()
	p := &processClient{cmd: processCommand(t, ctx, baseURL, processTestToken)}
	p.cmd.Stderr = &p.stderr
	client := sdk.NewClient(&sdk.Implementation{Name: "synthetic-process-test", Version: "1.0"}, nil)
	// Pin a supported version that exercises initialize, rather than only the
	// newer SDK's server/discover shortcut. CommandTransport uses OS pipes.
	session, err := client.Connect(ctx, &sdk.CommandTransport{
		Command: p.cmd, TerminateDuration: 2 * time.Second,
	}, &sdk.ClientSessionOptions{ProtocolVersion: processProtocolVersion})
	if err != nil {
		t.Fatalf("could not initialize subprocess MCP connection: %v", err)
	}
	p.session = session
	t.Cleanup(func() { _ = session.Close() })
	return p
}

func (p *processClient) close(t *testing.T) {
	t.Helper()
	// Close before t.Context is cancelled by cleanup: a killed process must not
	// masquerade as a successful client-initiated, stdin-EOF shutdown.
	if err := p.session.Close(); err != nil {
		t.Fatalf("subprocess did not close gracefully: %v", err)
	}
	if p.cmd.ProcessState == nil || !p.cmd.ProcessState.Success() {
		t.Fatal("subprocess did not exit successfully after the client closed stdin")
	}
	if p.stderr.Len() != 0 {
		t.Fatal("successful subprocess emitted unexpected diagnostics")
	}
}

func TestMCPExecutableSDKClient(t *testing.T) {
	cases := []struct {
		name, args, method, path string
		query                    url.Values
		body                     map[string]string
		response                 string
	}{
		{
			name: "get_system_status", method: http.MethodGet, path: "/api/v1/system",
			response: `{"version":"synthetic-process","storage":{"configured":true}}`,
		},
		{
			name: "search_conversations", method: http.MethodGet, path: "/api/v1/conversations",
			args:     `{"q":"项目 & +","platform":"weixin","category":"colleague","unread":true,"pinned":false,"archived":false,"limit":2,"cursor":"search cursor+/"}`,
			query:    url.Values{"q": {"项目 & +"}, "platform": {"weixin"}, "category": {"colleague"}, "unread": {"true"}, "pinned": {"false"}, "archived": {"false"}, "limit": {"2"}, "cursor": {"search cursor+/"}},
			response: `{"items":[{"id":"conversation-1","title":"合成项目"}],"next_cursor":"search-next"}`,
		},
		{
			name: "get_conversation_messages", method: http.MethodGet, path: "/api/v1/conversations/conversation-1/messages",
			args:     `{"conversation_id":"conversation-1","limit":2,"cursor":"synthetic-cursor+/="}`,
			query:    url.Values{"limit": {"2"}, "cursor": {"synthetic-cursor+/="}},
			response: `{"items":[{"id":"message-1","body":"合成项目消息"}],"next_cursor":"next-page"}`,
		},
		{
			name: "get_wechat_sessions", args: `{}`, method: http.MethodGet, path: "/api/v1/connections/wechat-db/sessions",
			response: `{"items":[{"peer_id":"@@synthetic-room","name":"合成群聊"}],"read_only":true}`,
		},
		{
			name: "read_wechat_messages", method: http.MethodPost, path: "/api/v1/connections/wechat-db/read",
			args:     `{"peer_id":"@@synthetic-room","keyword":" 合成项目 "}`,
			body:     map[string]string{"peer_id": "@@synthetic-room", "keyword": "合成项目"},
			response: `{"inserted":1,"sending_enabled":false,"source":"synthetic-only"}`,
		},
		{
			name: "get_latest_digest", args: `{}`, method: http.MethodGet, path: "/api/v1/digest/latest",
			response: `{"report":{"summary":"合成摘要","needs_attention":1}}`,
		},
	}
	var requests atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+processTestToken {
			t.Error("the child process did not use the synthetic service token")
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Error("the child process did not request JSON")
		}
		for _, tc := range cases {
			if r.URL.Path != tc.path {
				continue
			}
			if r.Method != tc.method || r.URL.RawQuery != tc.query.Encode() {
				t.Errorf("unexpected backend request: %s %s", r.Method, r.URL)
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read synthetic backend request: %v", err)
			}
			if tc.body == nil {
				if len(body) != 0 {
					t.Error("GET unexpectedly carried a request body")
				}
			} else {
				var decoded map[string]string
				if json.Unmarshal(body, &decoded) != nil || !reflect.DeepEqual(decoded, tc.body) {
					t.Error("selected synthetic peer and trimmed keyword were not forwarded exactly")
				}
				if r.Header.Get("Content-Type") != "application/json" {
					t.Error("WeChat read request did not have a JSON content type")
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, tc.response)
			return
		}
		t.Errorf("unexpected backend path %q", r.URL.Path)
		http.NotFound(w, r)
	}))
	t.Cleanup(backend.Close)
	ctx := processTestContext(t)
	p := processSDKClient(t, ctx, backend.URL)
	initialized := p.session.InitializeResult()
	if initialized == nil || initialized.ServerInfo == nil || initialized.ServerInfo.Name != "social-intelligence-os" || initialized.ServerInfo.Version == "" || initialized.ProtocolVersion != processProtocolVersion {
		t.Fatal("subprocess did not negotiate a named MCP server and the requested protocol version")
	}
	if initialized.Capabilities == nil || initialized.Capabilities.Tools == nil || !strings.Contains(initialized.Instructions, "不可信数据") {
		t.Fatal("subprocess initialization omitted tools or its untrusted-data warning")
	}
	listed, err := p.session.ListTools(ctx, &sdk.ListToolsParams{})
	if err != nil {
		t.Fatalf("could not discover subprocess tools: %v", err)
	}
	if len(listed.Tools) != len(cases) || listed.NextCursor != "" {
		t.Fatal("expected exactly six tools without another discovery page")
	}
	expected := make(map[string]bool, len(cases))
	for _, tc := range cases {
		expected[tc.name] = true
	}
	for _, tool := range listed.Tools {
		if !expected[tool.Name] {
			t.Fatalf("unexpected or duplicated exposed tool %q", tool.Name)
		}
		delete(expected, tool.Name)
		if tool.Annotations == nil || tool.Annotations.ReadOnlyHint != (tool.Name != "read_wechat_messages") || tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint || tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint != (tool.Name == "read_wechat_messages") {
			t.Fatalf("incorrect tool effect annotations for %q", tool.Name)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("initialization or tool discovery unexpectedly contacted the backend")
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := &sdk.CallToolParams{Name: tc.name}
			if tc.args != "" {
				params.Arguments = json.RawMessage(tc.args)
			}
			result, err := p.session.CallTool(ctx, params)
			if err != nil {
				t.Fatalf("subprocess tool call failed: %v", err)
			}
			text := processResultText(t, result)
			if result.IsError || !json.Valid([]byte(text)) || text != tc.response {
				t.Fatal("subprocess did not preserve the complete synthetic JSON response")
			}
		})
	}
	if requests.Load() != int32(len(cases)) {
		t.Fatalf("want exactly six backend requests, got %d", requests.Load())
	}
	if err := p.session.Ping(ctx, nil); err != nil {
		t.Fatalf("subprocess stopped responding after tool calls: %v", err)
	}
	p.close(t)
}

func processResultText(t *testing.T, result *sdk.CallToolResult) string {
	t.Helper()
	if result == nil || len(result.Content) != 1 {
		t.Fatal("expected a single MCP text result")
	}
	content, ok := result.Content[0].(*sdk.TextContent)
	if !ok {
		t.Fatal("expected MCP text content")
	}
	return content.Text
}

func TestMCPExecutableInvalidArgumentsNeverReachHTTP(t *testing.T) {
	const privateArgument = "synthetic-private-argument"
	var requests atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "synthetic backend must not be called", http.StatusInternalServerError)
	}))
	t.Cleanup(backend.Close)
	ctx := processTestContext(t)
	p := processSDKClient(t, ctx, backend.URL)
	cases := []struct{ name, tool, args string }{
		{"unknown_tool", "send_message", `{}`},
		{"status_unsupported_field", "get_system_status", `{"url":"synthetic-private-argument"}`},
		{"sessions_unsupported_field", "get_wechat_sessions", `{"token":"synthetic-private-argument"}`},
		{"digest_unsupported_field", "get_latest_digest", `{"sql":"synthetic-private-argument"}`},
		{"null_object", "search_conversations", `null`},
		{"array_object", "search_conversations", `[]`},
		{"duplicate_keys", "search_conversations", `{"q":"first","q":"synthetic-private-argument"}`},
		{"null_string", "search_conversations", `{"q":null}`},
		{"null_boolean", "search_conversations", `{"unread":null}`},
		{"string_boolean", "search_conversations", `{"unread":"true"}`},
		{"unsupported_platform", "search_conversations", `{"platform":"synthetic-private-argument"}`},
		{"unsupported_category", "search_conversations", `{"category":"synthetic-private-argument"}`},
		{"limit_too_small", "search_conversations", `{"limit":0}`},
		{"limit_too_large", "search_conversations", `{"limit":101}`},
		{"fractional_limit", "search_conversations", `{"limit":1.5}`},
		{"control_character", "search_conversations", `{"q":"line\nbreak"}`},
		{"missing_conversation", "get_conversation_messages", `{}`},
		{"conversation_path_traversal", "get_conversation_messages", `{"conversation_id":"../synthetic-private-argument"}`},
		{"missing_peer", "read_wechat_messages", `{}`},
		{"peer_path", "read_wechat_messages", `{"peer_id":"C:\\synthetic-private-argument"}`},
		{"null_keyword", "read_wechat_messages", `{"peer_id":"@@synthetic-room","keyword":null}`},
		{"unsupported_read_field", "read_wechat_messages", `{"peer_id":"@@synthetic-room","path":"synthetic-private-argument"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := p.session.CallTool(ctx, &sdk.CallToolParams{Name: tc.tool, Arguments: json.RawMessage(tc.args)})
			var rpcError *jsonrpc.Error
			if !errors.As(err, &rpcError) || rpcError.Code != jsonrpc.CodeInvalidParams {
				t.Fatalf("want JSON-RPC invalid parameters, got %v", err)
			}
			if strings.Contains(err.Error(), privateArgument) || strings.Contains(err.Error(), processTestToken) {
				t.Fatal("argument validation exposed a sensitive synthetic value")
			}
		})
	}
	if err := p.session.Ping(ctx, nil); err != nil {
		t.Fatalf("invalid arguments broke the subprocess session: %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid arguments reached HTTP %d times", requests.Load())
	}
	p.close(t)
}

func TestMCPExecutableBackendUnauthorizedIsSafeToolError(t *testing.T) {
	const privateError = "synthetic-backend-error-must-not-be-exposed"
	var requests atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/digest/latest" || r.Header.Get("Authorization") != "Bearer "+processTestToken {
			t.Error("unexpected synthetic authorization request")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": privateError, "token": processTestToken})
	}))
	t.Cleanup(backend.Close)
	ctx := processTestContext(t)
	p := processSDKClient(t, ctx, backend.URL)
	result, err := p.session.CallTool(ctx, &sdk.CallToolParams{Name: "get_latest_digest"})
	if err != nil {
		t.Fatalf("backend 401 should be a tool result, not a protocol failure: %v", err)
	}
	text := processResultText(t, result)
	if !result.IsError || text != "local social service returned HTTP 401" || strings.Contains(text, privateError) || strings.Contains(text, processTestToken) {
		t.Fatal("backend authorization error was not summarized safely")
	}
	if err := p.session.Ping(ctx, nil); err != nil {
		t.Fatalf("connection did not survive a backend error: %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("want one backend authorization request, got %d", requests.Load())
	}
	p.close(t)
}

func TestMCPExecutableCancellationReachesHTTP(t *testing.T) {
	ctx := processTestContext(t)
	started, cancelled := make(chan struct{}), make(chan struct{})
	var requests atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) != 1 || r.Method != http.MethodGet || r.URL.Path != "/api/v1/system" || r.Header.Get("Authorization") != "Bearer "+processTestToken {
			t.Error("unexpected synthetic cancellation request")
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		close(started)
		select {
		case <-r.Context().Done():
			close(cancelled)
		case <-ctx.Done():
			// Bound httptest shutdown even if cancellation propagation regresses.
		}
	}))
	t.Cleanup(backend.Close)
	p := processSDKClient(t, ctx, backend.URL)
	callCtx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		_, err := p.session.CallTool(callCtx, &sdk.CallToolParams{Name: "get_system_status"})
		done <- err
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("subprocess tool call did not reach the synthetic HTTP backend")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("want a cancelled client call, got %v", err)
		}
	case <-ctx.Done():
		t.Fatal("cancelled subprocess client call did not return before its deadline")
	}
	select {
	case <-cancelled:
	case <-ctx.Done():
		t.Fatal("MCP cancellation did not cross stdio and cancel the HTTP request")
	}
	if err := p.session.Ping(ctx, nil); err != nil {
		t.Fatalf("cancellation broke the subprocess session: %v", err)
	}
	p.close(t)
}

func TestMCPExecutableConfigurationFailures(t *testing.T) {
	const privateURL = "synthetic-private-url-value"
	const shortToken = "synthetic-short-token"
	var requests atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "startup must not contact HTTP", http.StatusInternalServerError)
	}))
	t.Cleanup(backend.Close)
	cases := []struct{ name, baseURL, token, diagnostic string }{
		{"missing_token", backend.URL, "", "SOCIALD_API_TOKEN"},
		{"short_token", backend.URL, shortToken, "SOCIALD_API_TOKEN"},
		{"whitespace_token", backend.URL, processTestToken + "\t", "SOCIALD_API_TOKEN"},
		{"url_credentials", "http://synthetic-user:" + privateURL + "@" + strings.TrimPrefix(backend.URL, "http://"), processTestToken, "SOCIALD_URL"},
		{"url_path", backend.URL + "/" + privateURL, processTestToken, "SOCIALD_URL"},
		{"url_query", backend.URL + "?token=" + privateURL, processTestToken, "SOCIALD_URL"},
		{"malformed_url", backend.URL + "/%" + privateURL, processTestToken, "SOCIALD_URL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := processTestContext(t)
			cmd := processCommand(t, ctx, tc.baseURL, tc.token)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			if ctx.Err() != nil {
				t.Fatal("configuration failure did not exit before its deadline")
			}
			var exitError *exec.ExitError
			if !errors.As(err, &exitError) || exitError.ExitCode() != 2 {
				t.Fatalf("invalid configuration should exit with code 2: %v", err)
			}
			if stdout.Len() != 0 {
				t.Fatal("startup diagnostic polluted the MCP stdout stream")
			}
			if !strings.Contains(stderr.String(), tc.diagnostic) {
				t.Fatal("startup failure had no actionable setting-name diagnostic")
			}
			for _, secret := range []string{processTestToken, shortToken, privateURL, "synthetic-user", tc.baseURL} {
				if strings.Contains(stderr.String(), secret) {
					t.Fatal("startup stderr exposed a sensitive synthetic configuration value")
				}
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatalf("configuration failures unexpectedly contacted HTTP %d times", requests.Load())
	}
}

func TestMCPExecutableCleanEOFDoesNotPrintPASS(t *testing.T) {
	var requests atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "EOF must not contact HTTP", http.StatusInternalServerError)
	}))
	t.Cleanup(backend.Close)
	ctx := processTestContext(t)
	cmd := processCommand(t, ctx, backend.URL, processTestToken)
	var stdout, stderr bytes.Buffer
	cmd.Stdin = strings.NewReader("")
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("clean stdin EOF should exit successfully: %v", err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatal("clean EOF emitted diagnostics or test-framework PASS on the protocol stream")
	}
	if requests.Load() != 0 {
		t.Fatal("clean EOF unexpectedly contacted the HTTP backend")
	}
}
