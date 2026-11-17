//go:build windows

package integration_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/qilinbro/msg-manager/internal/api"
	"github.com/qilinbro/msg-manager/internal/config"
	"github.com/qilinbro/msg-manager/internal/localcredentials/credentialstest"
	"github.com/qilinbro/msg-manager/internal/store"
)

// Reopen the encrypted backend handler/store behind a stable synthetic listener
// and restart the real stdio MCP subprocess. This is not a real-user/background
// service acceptance test: every database, key, credential and message is fake.
func TestMCPSavedCredentialWithReopenedEncryptedInbox(t *testing.T) {
	type handlerSlot struct{ handler http.Handler }
	var active atomic.Pointer[handlerSlot]
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		active.Load().handler.ServeHTTP(w, r)
	}))
	t.Cleanup(service.Close)
	configuration := config.Config{
		Addr: service.Listener.Addr().String(), APIToken: token,
		DatabasePath: filepath.Join(t.TempDir(), "saved-mcp-synthetic.db"), DatabaseKey: strings.Repeat("b2", 32),
	}
	handler, err := api.NewHandler(configuration)
	if err != nil {
		t.Fatal(err)
	}
	active.Store(&handlerSlot{handler})
	t.Cleanup(func() {
		service.Close()
		_ = active.Load().handler.(interface{ Close() error }).Close()
	})
	batch := store.ImportRequest{Platform: "qq", AccountID: "saved-mcp-synthetic", Messages: []store.ImportMessage{
		{ExternalID: "saved-m1", PeerID: "saved-peer", PeerName: "合成持久凭据联系人", Direction: "incoming", Body: "合成凭据重启后仍可读取的加密消息", OccurredAt: "2026-01-01T10:00:00+08:00"},
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
	if err != nil || response.StatusCode != http.StatusOK || imported.Inserted != 1 {
		t.Fatal("could not seed the synthetic encrypted inbox")
	}

	path := credentialstest.Write(t, service.URL, token)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for attempt := range 2 {
		if attempt == 1 {
			if err := active.Load().handler.(interface{ Close() error }).Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := api.NewHandler(configuration)
			if err != nil {
				t.Fatal(err)
			}
			active.Store(&handlerSlot{reopened})
		}
		// nil means absent SOCIALD_API_TOKEN, not a present empty value.
		session, closeSession := mcpBridgeClientCredentials(t, service.URL, nil, path)
		var system api.SystemResponse
		mcpToolJSON(t, session, "get_system_status", nil, &system)
		if !system.Storage.Configured || system.Storage.Driver != "sqlcipher" || system.Automation.AutoSendEnabled {
			t.Fatal("saved credentials did not preserve authenticated encrypted-backend capabilities")
		}
		var conversations store.ConversationPage
		mcpToolJSON(t, session, "search_conversations", map[string]any{"q": "合成凭据重启", "platform": "qq"}, &conversations)
		if len(conversations.Items) != 1 || conversations.Items[0].PeerID != "saved-peer" || conversations.Items[0].UnreadCount != 1 {
			t.Fatal("saved-credential subprocess did not find the preserved synthetic conversation")
		}
		var messages store.MessagePage
		mcpToolJSON(t, session, "get_conversation_messages", map[string]any{"conversation_id": conversations.Items[0].ID}, &messages)
		if len(messages.Items) != 1 || messages.Items[0].Body != batch.Messages[0].Body {
			t.Fatal("saved-credential subprocess could not read the preserved synthetic encrypted message")
		}
		closeSession()
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("backend handler/store or MCP restart changed the saved credential")
	}
	ciphertext, err := os.ReadFile(configuration.DatabasePath)
	if err != nil || bytes.HasPrefix(ciphertext, []byte("SQLite format 3")) || bytes.Contains(ciphertext, []byte(batch.Messages[0].Body)) {
		t.Fatal("synthetic inbox was not retained as encrypted SQLCipher storage")
	}
}
