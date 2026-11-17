package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func sdkTestSession(t *testing.T, server *Server) *sdk.ClientSession {
	t.Helper()
	return sdkTestSessionWithLifetime(t, server, t.Context())
}

func sdkTestSessionWithLifetime(t *testing.T, server *Server, lifetime context.Context) *sdk.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := server.sdkServer(lifetime).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "synthetic-test-client", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestSDKClientToolsAndBackendCalls(t *testing.T) {
	const token = "synthetic-token-123456789012345678901234567890"
	var requests atomic.Int32
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("bearer token was not forwarded")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/system":
			io.WriteString(w, `{"service":"synthetic"}`)
		case "/api/v1/conversations":
			if r.URL.Query().Get("q") != "项目" || r.URL.Query().Get("limit") != "2" {
				t.Error("search parameters were not forwarded")
			}
			io.WriteString(w, `{"items":[{"id":"conversation-1"}],"next_cursor":null}`)
		case "/api/v1/conversations/conversation-1/messages":
			io.WriteString(w, `{"items":[{"body":"合成消息"}],"next_cursor":null}`)
		case "/api/v1/connections/wechat-db/sessions":
			io.WriteString(w, `{"items":[{"peer_id":"@@room","name":"项目群"}],"read_only":true}`)
		case "/api/v1/connections/wechat-db/read":
			var body map[string]string
			if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&body) != nil || body["peer_id"] != "@@room" || body["keyword"] != "项目" {
				t.Error("WeChat read must be a POST scoped to selected peer and keyword")
			}
			io.WriteString(w, `{"inserted":1,"sending_enabled":false}`)
		case "/api/v1/digest/latest":
			io.WriteString(w, `{"report":null}`)
		default:
			t.Error("unexpected backend route")
			http.NotFound(w, r)
		}
	}))
	defer service.Close()
	server := &Server{baseURL: service.URL, token: token, http: service.Client(), gate: make(chan struct{}, 4)}
	session := sdkTestSession(t, server)
	init := session.InitializeResult()
	if init.ServerInfo.Name != serverName || init.ProtocolVersion == "" || init.Capabilities.Tools == nil || !strings.Contains(init.Instructions, "不可信数据") {
		t.Fatalf("unexpected server handshake: %#v", init)
	}
	if err := session.Ping(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	list, err := session.ListTools(context.Background(), nil)
	if err != nil || len(list.Tools) != 6 || list.NextCursor != "" {
		t.Fatalf("tool discovery failed: %v %#v", err, list)
	}
	for _, tool := range list.Tools {
		wantReadOnly := tool.Name != "read_wechat_messages"
		if tool.Annotations == nil || tool.Annotations.ReadOnlyHint != wantReadOnly || tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint != !wantReadOnly {
			t.Errorf("incorrect effect annotations for %s", tool.Name)
		}
		if !wantReadOnly && (!strings.Contains(tool.Description, "外部服务") || !strings.Contains(tool.Description, "费用") || !strings.Contains(tool.Description, "日报")) {
			t.Error("WeChat import must disclose downstream model processing and costs")
		}
	}
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"get_system_status", nil, "synthetic"},
		{"search_conversations", map[string]any{"q": "项目", "limit": 2}, "conversation-1"},
		{"get_conversation_messages", map[string]any{"conversation_id": "conversation-1"}, "合成消息"},
		{"get_wechat_sessions", nil, "项目群"},
		{"read_wechat_messages", map[string]any{"peer_id": "@@room", "keyword": "项目"}, `"inserted":1`},
		{"get_latest_digest", nil, `"report":null`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := &sdk.CallToolParams{Name: tc.name}
			if tc.args != nil {
				params.Arguments = tc.args
			}
			result, err := session.CallTool(context.Background(), params)
			if err != nil || result.IsError || len(result.Content) != 1 {
				t.Fatalf("tool call failed: %v %#v", err, result)
			}
			content, ok := result.Content[0].(*sdk.TextContent)
			if !ok || !json.Valid([]byte(content.Text)) || !strings.Contains(content.Text, tc.want) {
				t.Fatalf("unexpected tool content: %#v", result.Content)
			}
		})
	}
	if requests.Load() != 6 {
		t.Errorf("want exactly six backend calls, got %d", requests.Load())
	}
}

type testDiscardCloser struct{ io.Writer }

func (testDiscardCloser) Close() error { return nil }

func TestSDKStdioFrameLimit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	reader, writer := io.Pipe()
	defer writer.Close()
	session, err := (&Server{}).sdkServer(ctx).Connect(ctx, &sdk.IOTransport{
		Reader: reader, Writer: testDiscardCloser{io.Discard}, MaxLineLength: maxToolMessage,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	go func() {
		_, _ = io.WriteString(writer, strings.Repeat(" ", maxToolMessage+1)+"\n")
		_ = writer.Close()
	}()
	done := make(chan error, 1)
	go func() { done <- session.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("oversized input frame did not fail the transport")
		}
	case <-ctx.Done():
		t.Fatal("oversized input frame did not terminate within deadline")
	}
}

func TestSDKRejectsInvalidArgumentsBeforeBackend(t *testing.T) {
	var requests atomic.Int32
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "should not be called", 500)
	}))
	defer service.Close()
	session := sdkTestSession(t, &Server{baseURL: service.URL, http: service.Client(), gate: make(chan struct{}, 4)})
	cases := []struct{ name, args string }{
		{"missing_tool", `{}`},
		{"get_system_status", `{"sql":"private-test-marker"}`},
		{"get_system_status", `null`},
		{"search_conversations", `{"q":"a","q":"b"}`},
		{"search_conversations", `{"unread":null}`},
		{"search_conversations", `{"platform":"unsupported"}`},
		{"search_conversations", `{"limit":0}`},
		{"get_conversation_messages", `{}`},
		{"get_conversation_messages", `{"conversation_id":"../data"}`},
		{"read_wechat_messages", `{"peer_id":"@@room","keyword":null}`},
	}
	for _, tc := range cases {
		_, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: tc.name, Arguments: json.RawMessage(tc.args)})
		var rpcErr *jsonrpc.Error
		if !errors.As(err, &rpcErr) || rpcErr.Code != jsonrpc.CodeInvalidParams {
			t.Errorf("%s(%s): want invalid parameters, got %v", tc.name, tc.args, err)
		}
		if err != nil && strings.Contains(err.Error(), "private-test-marker") {
			t.Error("invalid argument value leaked into error")
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid calls reached backend %d times", requests.Load())
	}
}

func TestSDKBackendFailureIsToolError(t *testing.T) {
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "private-backend-error-marker", http.StatusUnauthorized)
	}))
	defer service.Close()
	session := sdkTestSession(t, &Server{baseURL: service.URL, http: service.Client(), gate: make(chan struct{}, 4)})
	result, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: "get_system_status"})
	if err != nil || !result.IsError || len(result.Content) != 1 {
		t.Fatalf("backend failure should be an execution error: %v %#v", err, result)
	}
	text := result.Content[0].(*sdk.TextContent).Text
	if !strings.Contains(text, "401") || strings.Contains(text, "private-backend-error-marker") {
		t.Fatalf("backend failure was not safely summarized: %q", text)
	}
}

func TestSDKCancellationStopsBackendRequest(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-r.Context().Done():
			close(cancelled)
		case <-time.After(5 * time.Second):
			t.Error("backend request was not cancelled")
		}
	}))
	defer service.Close()
	session := sdkTestSession(t, &Server{baseURL: service.URL, http: service.Client(), gate: make(chan struct{}, 4)})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "get_system_status"})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("backend call did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Error("cancelled client call succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("client cancellation hung")
	}
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("MCP cancellation did not reach HTTP request context")
	}
}

func TestSDKServerLifetimeCancellationStopsBackendRequest(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-r.Context().Done():
			close(cancelled)
		case <-time.After(5 * time.Second):
			t.Error("backend request outlived the MCP server lifetime")
		}
	}))
	t.Cleanup(service.Close)
	lifetime, stop := context.WithCancel(t.Context())
	defer stop()
	session := sdkTestSessionWithLifetime(t, &Server{baseURL: service.URL, http: service.Client(), gate: make(chan struct{}, 4)}, lifetime)
	done := make(chan error, 1)
	go func() {
		result, err := session.CallTool(t.Context(), &sdk.CallToolParams{Name: "get_system_status"})
		if err == nil && !result.IsError {
			err = errors.New("request succeeded after server lifetime cancellation")
		}
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("backend call did not start")
	}
	stop()
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("server lifetime cancellation did not reach HTTP request context")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server lifetime cancellation hung the tool response")
	}
}

func TestParseArgumentsRejectsNonObjectsAndDuplicateKeys(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `true`, `{"q":"first","q":"second"}`, `{"q":1} {}`, `{"q":`, string([]byte{'{', '"', 0xff, '"', ':', '1', '}'})} {
		if _, err := parseArguments(json.RawMessage(raw)); err == nil {
			t.Errorf("invalid argument object accepted: %q", raw)
		}
	}
	for _, raw := range []string{"", `{}`, `{"q":"合成"}`} {
		if _, err := parseArguments(json.RawMessage(raw)); err != nil {
			t.Errorf("valid argument object rejected: %v", err)
		}
	}
}
