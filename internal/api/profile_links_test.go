package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qilinbro/msg-manager/internal/store"
)

func TestProfileLinksRequireAuthenticationConfirmationAndKeepOriginals(t *testing.T) {
	h := newEmailTestHandler(t)
	ids := []string{}
	for _, platform := range []string{"qq", "telegram"} {
		result, err := h.inbox.Import(store.ImportRequest{Platform: platform, AccountID: "synthetic-link-account", Messages: []store.ImportMessage{{ExternalID: "message-1", PeerID: "synthetic-peer", PeerName: "同名测试联系人", Direction: "incoming", Body: "同名不代表同一个人", OccurredAt: "2026-01-01T00:00:00Z"}}})
		if err != nil || result.Conversations != 1 {
			t.Fatal("import links fixture", err)
		}
		page, err := h.inbox.Conversations(store.Filter{Platform: platform})
		if err != nil || len(page.Items) != 1 {
			t.Fatal(err)
		}
		ids = append(ids, page.Items[0].ID)
	}
	route := "/api/v1/conversations/" + ids[0] + "/profile-links"
	for _, action := range []struct{ method, path, body string }{{"GET", route, ""}, {"POST", route, `{"target_conversation_id":"` + ids[1] + `","confirmed":true}`}, {"DELETE", route + "/" + ids[1], ""}} {
		if w := emailTestRequest(h, action.method, action.path, action.body, false); w.Code != 401 {
			t.Fatal("unauthenticated link access", action.method, w.Code)
		}
	}
	if w := emailTestRequest(h, "POST", route, `{"target_conversation_id":"`+ids[1]+`","confirmed":false}`, true); w.Code != 409 {
		t.Fatal("unconfirmed same name association accepted", w.Code, w.Body.String())
	}
	if w := emailTestRequest(h, "POST", route, `{"target_conversation_id":"`+ids[1]+`","confirmed":true,"api_key":"invalid-extra"}`, true); w.Code != 400 {
		t.Fatal("unexpected link field accepted", w.Code)
	}
	w := emailTestRequest(h, "POST", route, `{"target_conversation_id":"`+ids[1]+`","confirmed":true}`, true)
	var links store.ProfileLinks
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &links) != nil || len(links.Linked) != 1 || !strings.Contains(links.DocumentMarkdown, "联合档案") {
		t.Fatal("confirmed link failed", w.Code, w.Body.String())
	}
	w = emailTestRequest(h, "DELETE", route+"/"+ids[1], "", true)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &links) != nil || len(links.Linked) != 0 || len(links.Candidates) != 1 {
		t.Fatal("unlink failed", w.Code, w.Body.String())
	}
	page, err := h.inbox.Conversations(store.Filter{})
	if err != nil || len(page.Items) != 2 {
		t.Fatal("link/unlink mutated original chats", err)
	}
}
