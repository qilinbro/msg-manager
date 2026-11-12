package integration_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qilinbro/msg-manager/internal/api"
	"github.com/qilinbro/msg-manager/internal/config"
	"github.com/qilinbro/msg-manager/internal/store"
)

func inboxHandler(t *testing.T, path string) http.Handler {
	t.Helper()
	h, err := api.NewHandler(config.Config{Addr: config.DefaultAddr, APIToken: token, DatabasePath: path, DatabaseKey: strings.Repeat("a1", 32)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.(interface{ Close() error }).Close() })
	return h
}

func inboxRequest(h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	var reader io.Reader
	if raw, ok := body.(string); ok {
		reader = strings.NewReader(raw)
	} else if body != nil {
		raw, _ := json.Marshal(body)
		reader = strings.NewReader(string(raw))
	}
	r := httptest.NewRequest(method, "http://"+config.DefaultAddr+path, reader)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://127.0.0.1:1420")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestUnifiedInboxImportManageAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inbox.db")
	h := inboxHandler(t, path)
	for _, platform := range []string{"qq", "weixin", "telegram", "shanliao"} {
		batch := store.ImportRequest{Platform: platform, AccountID: "9007199254740993", AccountName: "合成账号", Messages: []store.ImportMessage{{ExternalID: "m1", PeerID: "p1", PeerName: "合成联系人", Direction: "incoming", Body: "会议材料", OccurredAt: "2026-01-01T10:00:00+08:00"}, {ExternalID: "m2", PeerID: "p1", PeerName: "合成联系人", Direction: "outgoing", Body: "收到", OccurredAt: "2026-01-01T02:01:00Z"}}}
		w := inboxRequest(h, http.MethodPost, "/api/v1/imports/messages", batch)
		if w.Code != 200 {
			t.Fatalf("import %s status=%d body=%s", platform, w.Code, w.Body.String())
		}
		var result store.ImportResult
		_ = json.Unmarshal(w.Body.Bytes(), &result)
		if result.Inserted != 2 || result.Conversations != 1 {
			t.Fatal(result)
		}
		duplicate := inboxRequest(h, http.MethodPost, "/api/v1/imports/messages", batch)
		_ = json.Unmarshal(duplicate.Body.Bytes(), &result)
		if result.Inserted != 0 || result.Duplicates != 2 {
			t.Fatal("import not deduplicated")
		}
	}
	w := inboxRequest(h, http.MethodGet, "/api/v1/conversations?q=会议&limit=2", nil)
	var conversations store.ConversationPage
	_ = json.Unmarshal(w.Body.Bytes(), &conversations)
	if w.Code != 200 || len(conversations.Items) != 2 || conversations.NextCursor == nil {
		t.Fatalf("conversation page: %d %s", w.Code, w.Body.String())
	}
	id := conversations.Items[0].ID
	messagesResponse := inboxRequest(h, http.MethodGet, "/api/v1/conversations/"+id+"/messages?limit=1", nil)
	var messages store.MessagePage
	_ = json.Unmarshal(messagesResponse.Body.Bytes(), &messages)
	if messagesResponse.Code != 200 || len(messages.Items) != 1 || messages.Items[0].Body != "收到" || messages.NextCursor == nil {
		t.Fatalf("messages %s", messagesResponse.Body.String())
	}
	w = inboxRequest(h, http.MethodPatch, "/api/v1/conversations/"+id, `{"read":true,"pinned":true,"archived":true}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = inboxRequest(h, http.MethodGet, "/api/v1/conversations?archived=true&pinned=true", nil)
	_ = json.Unmarshal(w.Body.Bytes(), &conversations)
	if len(conversations.Items) != 1 || conversations.Items[0].UnreadCount != 0 {
		t.Fatal(w.Body.String())
	}
	_ = h.(interface{ Close() error }).Close()
	h = inboxHandler(t, path)
	w = inboxRequest(h, http.MethodGet, "/api/v1/conversations?archived=true", nil)
	_ = json.Unmarshal(w.Body.Bytes(), &conversations)
	if w.Code != 200 || len(conversations.Items) != 1 || conversations.Items[0].ID != id {
		t.Fatal("inbox not persistent")
	}
	w = inboxRequest(h, http.MethodGet, "/api/v1/system", nil)
	var system api.SystemResponse
	_ = json.Unmarshal(w.Body.Bytes(), &system)
	if !system.Storage.Configured || system.Storage.Driver != "sqlcipher" || system.Stage != "unified_inbox" || system.Automation.AutoSendEnabled || !system.Automation.Paused {
		t.Fatal("wrong capability state")
	}
	checkError(t, inboxRequest(h, http.MethodPost, "/api/v1/send", `{}`), 501, "NOT_IMPLEMENTED")
}

func TestInboxInputAuthScopeAndCORSBoundaries(t *testing.T) {
	h := inboxHandler(t, filepath.Join(t.TempDir(), "inbox.db"))
	for _, path := range []string{"/api/v1/inbox", "/api/v1/conversations", "/api/v1/inbox/connections"} {
		checkError(t, request(h, http.MethodGet, path, nil), 401, "UNAUTHORIZED")
	}
	for _, body := range []string{`null`, `[]`, `{}`, `{"platform":"qq","platform":"telegram"}`, `{"workspace_id":"other"}`, `{"messages":[]} {}`} {
		checkError(t, inboxRequest(h, http.MethodPost, "/api/v1/imports/messages", body), 400, "INVALID_REQUEST")
	}
	checkError(t, inboxRequest(h, http.MethodPatch, "/api/v1/conversations/00000000-0000-0000-0000-000000000000", `{"read":true,"send":true}`), 400, "INVALID_REQUEST")
	for _, query := range []string{"?platform=other", "?q=x&q=y", "?unread=1", "?limit=101", "?account_id=other", "?q=%zz"} {
		checkError(t, inboxRequest(h, http.MethodGet, "/api/v1/conversations"+query, nil), 400, "INVALID_REQUEST")
	}
	checkError(t, inboxRequest(h, http.MethodGet, "/api/v1/conversations?workspace_id=other", nil), 403, "SCOPE_DENIED")
	for _, entry := range []struct {
		path, method string
		want         int
	}{{"/api/v1/imports/messages", "POST", 204}, {"/api/v1/conversations/00000000-0000-0000-0000-000000000000", "PATCH", 204}, {"/api/v1/send", "POST", 403}, {"/api/v1/conversations", "DELETE", 403}} {
		w := request(h, http.MethodOptions, entry.path, map[string]string{"Origin": "http://127.0.0.1:1420", "Access-Control-Request-Method": entry.method, "Access-Control-Request-Headers": "authorization,content-type"})
		if w.Code != entry.want {
			t.Fatalf("CORS route %s %d", entry.path, w.Code)
		}
	}
	disabled := newHandler(t)
	checkError(t, inboxRequest(disabled, http.MethodPost, "/api/v1/imports/messages", `{}`), 503, "STORAGE_DISABLED")
	status := inboxRequest(disabled, http.MethodGet, "/api/v1/inbox", nil)
	if status.Code != 200 || !strings.Contains(status.Body.String(), `"storage_enabled":false`) {
		t.Fatal(status.Body.String())
	}
	checkError(t, inboxRequest(h, http.MethodPost, "/api/v1/imports/messages", strings.Repeat(" ", 8<<20+1)), 413, "PAYLOAD_TOO_LARGE")
}

func TestTelegramExportImportAPI(t *testing.T) {
	h := inboxHandler(t, filepath.Join(t.TempDir(), "inbox.db"))
	input := `{"account_id":"101","account_name":"合成TG账号","export":{"name":"合成好友","type":"personal_chat","id":202,"messages":[{"id":1,"type":"message","from_id":"user202","date_unixtime":"1704067200","text":["会议",{"type":"bold","text":"安排"}]},{"id":2,"type":"message","from_id":"user101","date_unixtime":"1704067300","photo":"photo.jpg","text":"图片说明"}]}}`
	w := inboxRequest(h, http.MethodPost, "/api/v1/imports/telegram", input)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"inserted":1`) || !strings.Contains(w.Body.String(), `"skipped":1`) {
		t.Fatal(w.Body.String())
	}
	checkError(t, inboxRequest(h, http.MethodPost, "/api/v1/imports/telegram", strings.Replace(input, `"personal_chat"`, `"private_group"`, 1)), 400, "INVALID_REQUEST")
}
