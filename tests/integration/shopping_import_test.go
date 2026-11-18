package integration_test

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/qilinbro/msg-manager/internal/store"
)

func TestShoppingPrivateChatImportAndSearch(t *testing.T) {
	handler := inboxHandler(t, filepath.Join(t.TempDir(), "shopping.db"))
	platforms := []string{"xianyu", "taobao", "jd", "pinduoduo"}
	var inbox struct {
		Platforms []struct {
			ID         string `json:"id"`
			LiveStatus string `json:"live_status"`
		} `json:"platforms"`
	}
	status := inboxRequest(handler, http.MethodGet, "/api/v1/inbox", nil)
	if status.Code != http.StatusOK || json.Unmarshal(status.Body.Bytes(), &inbox) != nil {
		t.Fatal(status.Code, status.Body.String())
	}
	capabilities := make(map[string]string)
	for _, platform := range inbox.Platforms {
		capabilities[platform.ID] = platform.LiveStatus
	}

	batch := store.ImportRequest{
		AccountID: "synthetic-buyer",
		Messages: []store.ImportMessage{
			{ExternalID: "msg-1", PeerID: "chat-a", PeerName: "示例会话", Direction: "incoming", Body: "小票袋已放好", OccurredAt: "2026-11-12T08:00:00+08:00"},
		},
	}
	post := func(request store.ImportRequest) store.ImportResult {
		t.Helper()
		response := inboxRequest(handler, http.MethodPost, "/api/v1/imports/messages", request)
		var result store.ImportResult
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil {
			t.Fatal(response.Code, response.Body.String())
		}
		return result
	}
	ids := make(map[string]bool)
	for _, platform := range platforms {
		if capabilities[platform] != "import_only" {
			t.Fatalf("%s declares unexpected inbox capability %q", platform, capabilities[platform])
		}
		batch.Platform = platform
		first := post(batch)
		replayed := post(batch)
		if first.Inserted != 1 || first.AIQueued != 0 || replayed.Inserted != 0 || replayed.Duplicates != 1 {
			t.Fatal(platform, first, replayed)
		}
		search := inboxRequest(handler, http.MethodGet, "/api/v1/conversations?platform="+platform+"&q=%E5%B0%8F%E7%A5%A8", nil)
		var page store.ConversationPage
		if search.Code != http.StatusOK || json.Unmarshal(search.Body.Bytes(), &page) != nil || len(page.Items) != 1 {
			t.Fatal(search.Code, search.Body.String())
		}
		item := page.Items[0]
		if item.Platform != platform || item.AccountID != batch.AccountID || ids[item.ID] {
			t.Fatal("shopping platform/account identity was lost", item)
		}
		ids[item.ID] = true
		history := inboxRequest(handler, http.MethodGet, "/api/v1/conversations/"+item.ID+"/messages", nil)
		var messages store.MessagePage
		if history.Code != http.StatusOK || json.Unmarshal(history.Body.Bytes(), &messages) != nil || len(messages.Items) != 1 || messages.Items[0].Source != "import" {
			t.Fatal(history.Code, history.Body.String())
		}
	}
	batch.AccountID = "synthetic-store"
	if result := post(batch); result.Inserted != 1 || len(result.ConversationIDs) != 1 || ids[result.ConversationIDs[0]] {
		t.Fatal("shopping account identity was not isolated", result)
	}
	batch.Messages[0].ConversationKind = "group"
	if response := inboxRequest(handler, http.MethodPost, "/api/v1/imports/messages", batch); response.Code != http.StatusBadRequest {
		t.Fatal("shopping group was accepted", response.Code, response.Body.String())
	}
}
