package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestChatSearchForwardsStrictKindLiteralBodyAndPagination(t *testing.T) {
	const keyword = `中文 50% 字段_a O'Reilly "quoted"`
	for _, entry := range []struct {
		name string
		args map[string]any
		path string
		want url.Values
	}{
		{"search_conversations", map[string]any{"q": keyword, "conversation_kind": "group", "unread": false, "cursor": "synthetic-cursor"}, "/api/v1/conversations", url.Values{"q": {keyword}, "conversation_kind": {"group"}, "unread": {"false"}, "cursor": {"synthetic-cursor"}, "limit": {"50"}}},
		{"search_conversations", map[string]any{"conversation_kind": "private", "limit": 100}, "/api/v1/conversations", url.Values{"conversation_kind": {"private"}, "limit": {"100"}}},
		{"search_conversations", map[string]any{}, "/api/v1/conversations", url.Values{"limit": {"50"}}},
		{"get_conversation_messages", map[string]any{"conversation_id": "synthetic-conversation", "q": keyword, "cursor": "synthetic-cursor", "limit": 1}, "/api/v1/conversations/synthetic-conversation/messages", url.Values{"q": {keyword}, "cursor": {"synthetic-cursor"}, "limit": {"1"}}},
		{"get_conversation_messages", map[string]any{"conversation_id": "synthetic-conversation", "q": "   "}, "/api/v1/conversations/synthetic-conversation/messages", url.Values{"q": {"   "}, "limit": {"50"}}},
		{"get_conversation_messages", map[string]any{"conversation_id": "synthetic-conversation", "q": ""}, "/api/v1/conversations/synthetic-conversation/messages", url.Values{"q": {""}, "limit": {"50"}}},
	} {
		t.Run(entry.name+entry.want.Encode(), func(t *testing.T) {
			var calls atomic.Int32
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != entry.path || !reflect.DeepEqual(r.URL.Query(), entry.want) || r.Header.Get("Authorization") != "Bearer "+testToken || r.ContentLength > 0 {
					t.Error("literal filters escaped their authenticated fixed read path", r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"items":[],"next_cursor":"synthetic-next-page"}`))
			}))
			defer backend.Close()
			server := &Server{baseURL: backend.URL, token: testToken, http: localHTTPClient()}
			defer server.http.CloseIdleConnections()
			raw, _ := json.Marshal(entry.args)
			body, err := server.callTool(context.Background(), entry.name, arguments(t, string(raw)))
			if err != nil || !strings.Contains(string(body), `"next_cursor":"synthetic-next-page"`) || calls.Load() != 1 {
				t.Fatal("search proxy changed pagination or performed an extra request", err, calls.Load())
			}
		})
	}
}

func TestChatSearchRejectsInvalidKindBodyOrScopeBeforeBackend(t *testing.T) {
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer backend.Close()
	server := &Server{baseURL: backend.URL, token: testToken, http: localHTTPClient()}
	defer server.http.CloseIdleConnections()
	for _, value := range []any{"", "all", "GROUP", "group ", " private", nil, true, 1, []string{"group"}} {
		raw, _ := json.Marshal(map[string]any{"conversation_kind": value})
		if _, err := server.callTool(t.Context(), "search_conversations", arguments(t, string(raw))); err == nil {
			t.Fatal("invalid conversation kind reached backend", value)
		}
	}
	for _, name := range []string{"search_conversations", "get_conversation_messages"} {
		for _, q := range []any{nil, false, 1, []string{"body"}, strings.Repeat("a", 257), strings.Repeat("中", 86), "body\x00tail", "body\ntail"} {
			fields := map[string]any{"q": q}
			if name == "get_conversation_messages" {
				fields["conversation_id"] = "synthetic-conversation"
			}
			raw, _ := json.Marshal(fields)
			if _, err := server.callTool(t.Context(), name, arguments(t, string(raw))); err == nil {
				t.Fatal("invalid literal body query reached backend", name)
			}
		}
		for _, extra := range []string{"workspace_id", "account_id", "sql", "api_key", "url"} {
			fields := map[string]any{extra: "synthetic-forbidden-scope"}
			if name == "get_conversation_messages" {
				fields["conversation_id"] = "synthetic-conversation"
			}
			raw, _ := json.Marshal(fields)
			if _, err := server.callTool(t.Context(), name, arguments(t, string(raw))); err == nil || strings.Contains(err.Error(), "synthetic-forbidden-scope") {
				t.Fatal("unknown scope/credential field accepted or reflected", name, extra)
			}
		}
		for _, accepted := range []string{"", "   ", strings.Repeat("a", 256), strings.Repeat("中", 85) + "a"} {
			fields := map[string]any{"q": accepted}
			if name == "get_conversation_messages" {
				fields["conversation_id"] = "synthetic-conversation"
			}
			raw, _ := json.Marshal(fields)
			if err := validateArguments(name, arguments(t, string(raw))); err != nil {
				t.Fatal("valid UTF-8 byte-boundary search rejected", name, err)
			}
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid search arguments caused backend IO", calls.Load())
	}
}

func TestSDKChatSearchPublishesStrictOptionalFiltersAndRejectsDuplicates(t *testing.T) {
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+testToken || r.Method != http.MethodGet {
			t.Error("SDK search lost authenticated read-only transport")
		}
		switch r.URL.Path {
		case "/api/v1/conversations":
			if r.URL.Query().Get("conversation_kind") != "group" || r.URL.Query().Get("q") != "50%字段_a" {
				t.Error("SDK lost literal kind/body filters")
			}
		case "/api/v1/conversations/synthetic-conversation/messages":
			if r.URL.Query().Get("q") != "50%字段_a" || r.URL.Query().Get("cursor") != "synthetic-cursor" {
				t.Error("SDK lost body filter/cursor")
			}
		default:
			t.Error("SDK escaped the fixed search routes")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[],"next_cursor":null}`))
	}))
	defer backend.Close()
	session := sdkTestSession(t, &Server{baseURL: backend.URL, token: testToken, http: backend.Client(), gate: make(chan struct{}, 4)})
	list, err := session.ListTools(t.Context(), nil)
	if err != nil || len(list.Tools) != 6 {
		t.Fatal("MCP read surface unexpectedly expanded", err)
	}
	found := 0
	for _, tool := range list.Tools {
		if tool.Name != "search_conversations" && tool.Name != "get_conversation_messages" {
			continue
		}
		found++
		var schema struct {
			AdditionalProperties *bool                      `json:"additionalProperties"`
			Properties           map[string]json.RawMessage `json:"properties"`
			Required             []string                   `json:"required"`
		}
		raw, _ := json.Marshal(tool.InputSchema)
		if json.Unmarshal(raw, &schema) != nil || schema.AdditionalProperties == nil || *schema.AdditionalProperties || tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Fatal("search schema is not strict/read-only", tool.Name)
		}
		var keyword struct {
			Type string `json:"type"`
			Max  int    `json:"maxLength"`
		}
		if json.Unmarshal(schema.Properties["q"], &keyword) != nil || keyword.Type != "string" || keyword.Max != 256 {
			t.Fatal("SDK omitted bounded literal q", tool.Name)
		}
		for _, field := range schema.Required {
			if field == "q" || field == "conversation_kind" {
				t.Fatal("new filters must remain optional", tool.Name)
			}
		}
		if tool.Name == "search_conversations" {
			var kind struct {
				Enum []string `json:"enum"`
			}
			if json.Unmarshal(schema.Properties["conversation_kind"], &kind) != nil || !reflect.DeepEqual(kind.Enum, []string{"private", "group"}) {
				t.Fatal("SDK did not publish the exact kind enum")
			}
		} else if schema.Properties["conversation_kind"] != nil {
			t.Fatal("message search accepted a cross-conversation kind override")
		}
	}
	if found != 2 {
		t.Fatal("SDK search tools missing")
	}
	for _, entry := range []struct{ name, args string }{
		{"search_conversations", `{"q":"50%字段_a","conversation_kind":"group"}`},
		{"get_conversation_messages", `{"conversation_id":"synthetic-conversation","q":"50%字段_a","cursor":"synthetic-cursor"}`},
	} {
		result, err := session.CallTool(t.Context(), &sdk.CallToolParams{Name: entry.name, Arguments: json.RawMessage(entry.args)})
		if err != nil || result.IsError {
			t.Fatal("SDK valid search rejected", entry.name, err)
		}
	}
	for _, entry := range []struct{ name, args string }{
		{"search_conversations", `{"conversation_kind":"private","conversation_kind":"group"}`},
		{"search_conversations", `{"conversation_kind":"all"}`},
		{"search_conversations", `{"conversation_kind":null}`},
		{"get_conversation_messages", `{"conversation_id":"synthetic-conversation","q":"a","q":"b"}`},
		{"get_conversation_messages", `{"conversation_id":"synthetic-conversation","q":null}`},
		{"get_conversation_messages", fmt.Sprintf(`{"conversation_id":"synthetic-conversation","q":%q}`, strings.Repeat("中", 86))},
	} {
		_, err := session.CallTool(t.Context(), &sdk.CallToolParams{Name: entry.name, Arguments: json.RawMessage(entry.args)})
		var rpcErr *jsonrpc.Error
		if !errors.As(err, &rpcErr) || rpcErr.Code != jsonrpc.CodeInvalidParams {
			t.Fatal("SDK invalid/duplicate search did not fail closed", entry.name, err)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("SDK performed extra IO for invalid search filters", calls.Load())
	}
}
