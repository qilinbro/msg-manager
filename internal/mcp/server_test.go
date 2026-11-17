package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testToken = "synthetic-token-123456789012345678901234567890"

func arguments(t *testing.T, text string) map[string]json.RawMessage {
	t.Helper()
	var args map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &args); err != nil {
		t.Fatal(err)
	}
	return args
}

func TestToolsForwardOnlyExpectedRequests(t *testing.T) {
	tests := []struct{ name, args, method, path, query, body string }{
		{"get_system_status", `{}`, "GET", "/api/v1/system", "", ""},
		{"search_conversations", `{"q":"项目","platform":"weixin","category":"colleague","unread":true,"pinned":false,"archived":false,"limit":2,"cursor":"abc"}`, "GET", "/api/v1/conversations", "archived=false&category=colleague&cursor=abc&limit=2&pinned=false&platform=weixin&q=%E9%A1%B9%E7%9B%AE&unread=true", ""},
		{"get_conversation_messages", `{"conversation_id":"conversation-1","cursor":"abc"}`, "GET", "/api/v1/conversations/conversation-1/messages", "cursor=abc&limit=50", ""},
		{"get_wechat_sessions", `{}`, "GET", "/api/v1/connections/wechat-db/sessions", "", ""},
		{"read_wechat_messages", `{"peer_id":"@@room","keyword":" 项目 "}`, "POST", "/api/v1/connections/wechat-db/read", "", `{"keyword":"项目","peer_id":"@@room"}`},
		{"get_latest_digest", `{}`, "GET", "/api/v1/digest/latest", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != tc.method || r.URL.Path != tc.path || r.URL.RawQuery != tc.query {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				if r.Header.Get("Authorization") != "Bearer "+testToken {
					t.Error("missing bearer token")
				}
				body, _ := io.ReadAll(r.Body)
				if string(body) != tc.body {
					t.Errorf("body = %s, want %s", body, tc.body)
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"synthetic":"测试记录"}`)
			}))
			defer backend.Close()
			t.Setenv("SOCIALD_URL", backend.URL)
			t.Setenv("SOCIALD_API_TOKEN", testToken)
			s, err := NewFromEnv()
			if err != nil {
				t.Fatal(err)
			}
			defer s.http.CloseIdleConnections()
			data, err := s.callTool(context.Background(), tc.name, arguments(t, tc.args))
			if err != nil || string(data) != `{"synthetic":"测试记录"}` || calls.Load() != 1 {
				t.Fatalf("result %s, err %v, calls %d", data, err, calls.Load())
			}
		})
	}
}

func TestInvalidArgumentsNeverReachBackend(t *testing.T) {
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer backend.Close()
	s := &Server{baseURL: backend.URL, token: testToken, http: localHTTPClient()}
	defer s.http.CloseIdleConnections()
	tests := []struct{ tool, args string }{
		{"get_system_status", `{"url":"http://example.com"}`},
		{"get_latest_digest", `{"sql":"select 1"}`},
		{"get_wechat_sessions", `{"token":"do-not-echo"}`},
		{"search_conversations", `{"q":null}`},
		{"search_conversations", `{"unread":null}`},
		{"search_conversations", `{"unread":"true"}`},
		{"search_conversations", `{"platform":"invalid"}`},
		{"search_conversations", `{"category":"invalid"}`},
		{"search_conversations", `{"limit":0}`},
		{"search_conversations", `{"limit":101}`},
		{"search_conversations", `{"limit":1.5}`},
		{"search_conversations", `{"limit":null}`},
		{"search_conversations", `{"q":"line\nbreak"}`},
		{"search_conversations", fmt.Sprintf(`{"q":%q}`, strings.Repeat("中", 86))},
		{"get_conversation_messages", `{}`},
		{"get_conversation_messages", `{"conversation_id":null}`},
		{"get_conversation_messages", `{"conversation_id":"../secret"}`},
		{"get_conversation_messages", fmt.Sprintf(`{"conversation_id":"ok","cursor":%q}`, strings.Repeat("c", 2049))},
		{"read_wechat_messages", `{"peer_id":"C:\\secret"}`},
		{"read_wechat_messages", `{"peer_id":" @@room"}`},
		{"read_wechat_messages", `{"peer_id":"@@room","keyword":null}`},
		{"read_wechat_messages", `{"peer_id":"@@room","keyword":"\t"}`},
		{"read_wechat_messages", `{"peer_id":"@@room","path":"secret"}`},
		{"read_wechat_messages", fmt.Sprintf(`{"peer_id":"@@room","keyword":%q}`, strings.Repeat("中", 129))},
		{"send_message", `{}`},
	}
	for i, tc := range tests {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			if _, err := s.callTool(context.Background(), tc.tool, arguments(t, tc.args)); err == nil {
				t.Fatal("invalid arguments accepted")
			} else if strings.Contains(err.Error(), "do-not-echo") {
				t.Fatal("argument secret was echoed")
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid calls reached backend: %d", calls.Load())
	}
	if err := validateArguments("read_wechat_messages", arguments(t, fmt.Sprintf(`{"peer_id":"@@room","keyword":%q}`, strings.Repeat("中", 128)))); err != nil {
		t.Fatal("128-character keyword rejected:", err)
	}
}

func TestEnvironmentBoundary(t *testing.T) {
	t.Setenv("SOCIALD_API_TOKEN", testToken)
	for _, base := range []string{"http://example.com:17890", "https://127.0.0.1:17890", "http://127.0.0.1:17890/path", "http://u:p@localhost:17890", "http://localhost:17890?", "http://localhost:17890?q=1", "http://localhost:17890/#secret", "http://localhost:17890#", "http://localhost:17890/#", "http://localhost:17890/%2f", "http://localhost:0", "http://localhost:65536", "http://localhost", "http://[::1]:17890", "http:localhost:17890"} {
		t.Setenv("SOCIALD_URL", base)
		if _, err := NewFromEnv(); err == nil {
			t.Errorf("accepted URL %q", base)
		}
	}
	t.Setenv("SOCIALD_URL", "http://localhost:17890/")
	s, err := NewFromEnv()
	if err != nil || s.baseURL != "http://127.0.0.1:17890" {
		t.Fatalf("localhost not canonicalized: %v", err)
	}
	for _, token := range []string{"", strings.Repeat("x", 31), strings.Repeat("x", 4097), testToken + "\n", " " + testToken, strings.Repeat("中", 32), strings.Repeat("x", 32) + "\x7f"} {
		t.Setenv("SOCIALD_API_TOKEN", token)
		if _, err := NewFromEnv(); err == nil {
			t.Error("invalid token accepted")
		}
	}
}

func TestHTTPResponseAndRedirectBoundaries(t *testing.T) {
	var redirected atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer other.Close()
	for _, tc := range []struct {
		name, contentType, body, want string
		code                          int
	}{
		{"redirect", "application/json", `{}`, "HTTP 307", 307},
		{"unauthorized", "application/json", `{"secret":"never-return-this"}`, "HTTP 401", 401},
		{"wrong-type", "text/html", `{}`, "non-JSON", 200},
		{"bad-json", "application/json", `<html>never-return-this</html>`, "invalid JSON", 200},
		{"too-large", "application/json", `{}` + strings.Repeat(" ", maxToolMessage), "exceeds 1 MiB", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				w.Header().Set("Location", other.URL)
				w.WriteHeader(tc.code)
				io.WriteString(w, tc.body)
			}))
			defer backend.Close()
			s := &Server{baseURL: backend.URL, token: testToken, http: localHTTPClient()}
			defer s.http.CloseIdleConnections()
			data, err := s.callTool(context.Background(), "get_system_status", nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "never-return-this") || len(data) != 0 {
				t.Fatalf("unsafe or wrong error: %v", err)
			}
		})
	}
	if redirected.Load() != 0 {
		t.Fatal("redirect followed")
	}
	if localHTTPClient().Transport.(*http.Transport).Proxy != nil {
		t.Fatal("environment proxy enabled")
	}
}

func TestCancellationAndConcurrencyBound(t *testing.T) {
	started, ended := make(chan struct{}), make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(ended) }))
	defer backend.Close()
	s := &Server{baseURL: backend.URL, token: testToken, http: localHTTPClient(), gate: make(chan struct{}, 1)}
	defer s.http.CloseIdleConnections()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := s.callTool(ctx, "get_system_status", nil); done <- err }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("request never started")
	}
	if _, err := s.callTool(context.Background(), "get_latest_digest", nil); err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatal("concurrency bound not enforced")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled request succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("request not cancelled")
	}
	select {
	case <-ended:
	case <-time.After(3 * time.Second):
		t.Fatal("backend did not see cancellation")
	}
}
