package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/qilinbro/msg-manager/internal/api"
	"github.com/qilinbro/msg-manager/internal/config"
	bridge "github.com/qilinbro/msg-manager/internal/mcp"
	"github.com/qilinbro/msg-manager/internal/store"
)

// The subprocess uses only an explicit synthetic environment and the public
// bridge entry point. Never load the user's backend configuration in this test.
func TestMCPBridgeSubprocess(t *testing.T) {
	if os.Getenv("SOCIALD_MCP_INTEGRATION_HELPER") != "1" {
		return
	}
	server, err := bridge.NewFromEnv()
	if err == nil {
		err = server.Run(t.Context())
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "synthetic MCP bridge failed")
		os.Exit(1)
	}
	// The testing package's PASS line must never enter the protocol stream.
	os.Exit(0)
}

func mcpBridgeClient(t *testing.T, baseURL, apiToken string) (*sdk.ClientSession, func()) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	t.Cleanup(cancel)
	command := exec.CommandContext(ctx, executable, "-test.run=^TestMCPBridgeSubprocess$")
	command.Env = []string{"SOCIALD_MCP_INTEGRATION_HELPER=1", "SOCIALD_URL=" + baseURL, "SOCIALD_API_TOKEN=" + apiToken}
	command.WaitDelay = 2 * time.Second
	var stderr bytes.Buffer
	command.Stderr = &stderr
	client := sdk.NewClient(&sdk.Implementation{Name: "synthetic-integration-client", Version: "1"}, nil)
	session, err := client.Connect(ctx, &sdk.CommandTransport{Command: command, TerminateDuration: 2 * time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, func() {
		t.Helper()
		if err := session.Close(); err != nil {
			t.Fatalf("MCP subprocess did not shut down cleanly: %v", err)
		}
		if command.ProcessState == nil || !command.ProcessState.Success() || stderr.Len() != 0 {
			t.Fatalf("unexpected MCP subprocess exit: %v; stderr=%q", command.ProcessState, stderr.String())
		}
	}
}

func mcpToolJSON(t *testing.T, session *sdk.ClientSession, name string, args any, target any) {
	t.Helper()
	result, err := session.CallTool(t.Context(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil || result.IsError || len(result.Content) != 1 {
		t.Fatalf("MCP %s failed: %v %#v", name, err, result)
	}
	text, ok := result.Content[0].(*sdk.TextContent)
	if !ok || json.Unmarshal([]byte(text.Text), target) != nil {
		t.Fatalf("MCP %s did not return backend JSON", name)
	}
}

func TestMCPBridgeWithAuthenticatedEncryptedInbox(t *testing.T) {
	service := httptest.NewUnstartedServer(nil)
	t.Cleanup(service.Close)
	handler, err := api.NewHandler(config.Config{
		Addr: service.Listener.Addr().String(), APIToken: token,
		DatabasePath: filepath.Join(t.TempDir(), "mcp-synthetic.db"), DatabaseKey: strings.Repeat("a1", 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.(interface{ Close() error }).Close() })
	service.Config.Handler = handler
	service.Start()
	// Close HTTP before the store, including when an assertion fails.
	t.Cleanup(service.Close)

	for _, platform := range []string{"qq", "telegram"} {
		batch := store.ImportRequest{Platform: platform, AccountID: "mcp-synthetic-account", AccountName: "合成账号", Messages: []store.ImportMessage{
			{ExternalID: "m1", PeerID: "mcp-synthetic-peer", PeerName: "合成 MCP 联系人", Direction: "incoming", Body: "合成会议材料", OccurredAt: "2026-01-01T10:00:00+08:00"},
			{ExternalID: "m2", PeerID: "mcp-synthetic-peer", PeerName: "合成 MCP 联系人", Direction: "outgoing", Body: "合成确认回复", OccurredAt: "2026-01-01T10:01:00+08:00"},
		}}
		raw, err := json.Marshal(batch)
		if err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, service.URL+"/api/v1/imports/messages", bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		response, err := service.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		var imported store.ImportResult
		err = json.NewDecoder(response.Body).Decode(&imported)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK || err != nil || imported.Inserted != 2 {
			t.Fatalf("synthetic seed import failed: %d %v %#v", response.StatusCode, err, imported)
		}
	}

	session, closeSession := mcpBridgeClient(t, service.URL, token)
	var system api.SystemResponse
	mcpToolJSON(t, session, "get_system_status", nil, &system)
	if !system.Storage.Configured || system.Storage.Driver != "sqlcipher" || system.Automation.AutoSendEnabled {
		t.Fatalf("MCP returned incorrect encrypted-backend capability state: %#v", system)
	}
	var conversations store.ConversationPage
	mcpToolJSON(t, session, "search_conversations", map[string]any{"q": "会议", "platform": "qq", "unread": true, "limit": 1}, &conversations)
	if len(conversations.Items) != 1 || conversations.Items[0].Platform != "qq" || conversations.Items[0].PeerID != "mcp-synthetic-peer" || conversations.Items[0].UnreadCount != 1 {
		t.Fatalf("MCP search did not preserve backend filtering: %#v", conversations)
	}
	conversationID := conversations.Items[0].ID
	// Equal source timestamps on two platforms exercise stable search cursors,
	// independently of the platform-scoped message lookup below.
	var firstSearch, secondSearch store.ConversationPage
	mcpToolJSON(t, session, "search_conversations", map[string]any{"q": "会议", "limit": 1}, &firstSearch)
	if len(firstSearch.Items) != 1 || firstSearch.NextCursor == nil {
		t.Fatalf("MCP first search page is incorrect: %#v", firstSearch)
	}
	mcpToolJSON(t, session, "search_conversations", map[string]any{"q": "会议", "limit": 1, "cursor": *firstSearch.NextCursor}, &secondSearch)
	if len(secondSearch.Items) != 1 || secondSearch.Items[0].ID == firstSearch.Items[0].ID || secondSearch.Items[0].Platform == firstSearch.Items[0].Platform || secondSearch.NextCursor != nil {
		t.Fatalf("MCP search cursor lost or repeated a platform conversation: %#v %#v", firstSearch, secondSearch)
	}
	var latest, older store.MessagePage
	mcpToolJSON(t, session, "get_conversation_messages", map[string]any{"conversation_id": conversationID, "limit": 1}, &latest)
	if len(latest.Items) != 1 || latest.Items[0].Body != "合成确认回复" || latest.NextCursor == nil {
		t.Fatalf("MCP first message page is incorrect: %#v", latest)
	}
	mcpToolJSON(t, session, "get_conversation_messages", map[string]any{"conversation_id": conversationID, "limit": 1, "cursor": *latest.NextCursor}, &older)
	if len(older.Items) != 1 || older.Items[0].Body != "合成会议材料" || older.Items[0].ConversationID != conversationID || older.NextCursor != nil {
		t.Fatalf("MCP message cursor crossed conversation or lost data: %#v", older)
	}
	var afterRead store.ConversationPage
	mcpToolJSON(t, session, "search_conversations", map[string]any{"platform": "qq", "unread": true}, &afterRead)
	if len(afterRead.Items) != 1 || afterRead.Items[0].UnreadCount != 1 {
		t.Fatal("read-only MCP queries mutated unread state")
	}
	var digest struct {
		Report json.RawMessage `json:"report"`
	}
	mcpToolJSON(t, session, "get_latest_digest", nil, &digest)
	if string(digest.Report) != "null" {
		t.Fatalf("fresh synthetic database must not invent a digest: %s", digest.Report)
	}
	// A configured local inbox does not grant access to an unconfigured WeChat
	// source. These calls must fail without probing real files or credentials.
	for _, name := range []string{"get_wechat_sessions", "read_wechat_messages"} {
		params := &sdk.CallToolParams{Name: name}
		if name == "read_wechat_messages" {
			params.Arguments = map[string]any{"peer_id": "wxid_synthetic"}
		}
		result, err := session.CallTool(t.Context(), params)
		if err != nil || !result.IsError || len(result.Content) != 1 {
			t.Fatalf("unconfigured WeChat source did not remain unavailable: %s %v %#v", name, err, result)
		}
		text, ok := result.Content[0].(*sdk.TextContent)
		if !ok || text.Text != "local social service returned HTTP 503" {
			t.Fatalf("unexpected WeChat failure summary: %#v", result.Content)
		}
	}
	closeSession()

	unauthorized, closeUnauthorized := mcpBridgeClient(t, service.URL, strings.Repeat("z", 40))
	result, err := unauthorized.CallTool(t.Context(), &sdk.CallToolParams{Name: "get_system_status"})
	if err != nil || !result.IsError || len(result.Content) != 1 {
		t.Fatalf("real backend token rejection was not a tool error: %v %#v", err, result)
	}
	text, ok := result.Content[0].(*sdk.TextContent)
	if !ok || text.Text != "local social service returned HTTP 401" {
		t.Fatalf("real backend authentication error was not safely summarized: %#v", result.Content)
	}
	closeUnauthorized()
}
