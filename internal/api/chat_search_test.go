package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/qilinbro/msg-manager/internal/config"
	"github.com/qilinbro/msg-manager/internal/store"
)

func chatSearchAPIImport(t *testing.T, h *handler, platform, account, peer, kind string, bodies ...string) string {
	t.Helper()
	request := store.ImportRequest{Platform: platform, AccountID: account, AccountName: "合成账户"}
	for index, body := range bodies {
		request.Messages = append(request.Messages, store.ImportMessage{
			ExternalID: fmt.Sprintf("message-%d", index), PeerID: peer, PeerName: "同名合成会话", ConversationKind: kind,
			Direction: "incoming", Body: body, OccurredAt: fmt.Sprintf("2026-01-01T00:00:%02dZ", index/3),
		})
	}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	response := emailTestRequest(h, http.MethodPost, "/api/v1/imports/messages", string(payload), true)
	if response.Code != http.StatusOK {
		t.Fatal("synthetic chat import failed", response.Code, response.Body.String())
	}
	var result store.ImportResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || len(result.ConversationIDs) != 1 {
		t.Fatal("synthetic chat import result", err, response.Body.String())
	}
	return result.ConversationIDs[0]
}

func chatSearchAPIGet(t *testing.T, h *handler, path string, page any) {
	t.Helper()
	response := emailTestRequest(h, http.MethodGet, path, "", true)
	if response.Code != http.StatusOK {
		t.Fatalf("GET %s: %d %s", path, response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), page); err != nil {
		t.Fatal("decode chat search page", err)
	}
}

func TestChatSearchAPIKindIntersectsQueryAndPagination(t *testing.T) {
	h := newEmailTestHandler(t)
	firstGroup := chatSearchAPIImport(t, h, "weixin", "account", "group-a", "group", "历史项目 中文", "最新预览")
	secondGroup := chatSearchAPIImport(t, h, "weixin", "account", "group-b", "group", "历史项目 中文", "最新预览")
	private := chatSearchAPIImport(t, h, "weixin", "account", "private", "private", "历史项目 中文")
	otherPlatform := chatSearchAPIImport(t, h, "qq", "account", "qq-group", "group", "历史项目 中文")
	archived := chatSearchAPIImport(t, h, "weixin", "account", "archived", "group", "历史项目 中文")
	for _, id := range []string{firstGroup, secondGroup, private, otherPlatform, archived} {
		patch := `{"pinned":true}`
		if id == archived {
			patch = `{"pinned":true,"archived":true}`
		}
		response := emailTestRequest(h, http.MethodPatch, "/api/v1/conversations/"+id, patch, true)
		if response.Code != http.StatusOK {
			t.Fatal("synthetic view setup failed", response.Code, response.Body.String())
		}
	}
	query := url.Values{"platform": {"weixin"}, "conversation_kind": {"group"}, "q": {"中文"}, "unread": {"true"}, "pinned": {"true"}, "category": {"unclassified"}, "limit": {"1"}}
	var first, second store.ConversationPage
	chatSearchAPIGet(t, h, "/api/v1/conversations?"+query.Encode(), &first)
	if len(first.Items) != 1 || first.NextCursor == nil || first.Items[0].ConversationKind != "group" || first.Items[0].LastMessage != "最新预览" || first.Items[0].MatchedMessage != "历史项目 中文" {
		t.Fatal("combined list search did not return a filtered latest-preview page", first)
	}
	query.Set("cursor", *first.NextCursor)
	chatSearchAPIGet(t, h, "/api/v1/conversations?"+query.Encode(), &second)
	if len(second.Items) != 1 || second.NextCursor != nil {
		t.Fatal("combined list search paging failed", second)
	}
	got, want := []string{first.Items[0].ID, second.Items[0].ID}, []string{firstGroup, secondGroup}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatal("combined query leaked/skipped another kind/platform/view", got, want)
	}
	for _, kind := range []string{"private", ""} {
		changed := make(url.Values, len(query))
		for key, values := range query {
			changed[key] = slices.Clone(values)
		}
		if kind == "" {
			changed.Del("conversation_kind")
		} else {
			changed.Set("conversation_kind", kind)
		}
		response := emailTestRequest(h, http.MethodGet, "/api/v1/conversations?"+changed.Encode(), "", true)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "INVALID_CURSOR") {
			t.Fatal("kind cursor reused across tabs", response.Code, response.Body.String())
		}
	}
	var privatePage, legacyPage, archivePage store.ConversationPage
	chatSearchAPIGet(t, h, "/api/v1/conversations?platform=weixin&conversation_kind=private&q="+url.QueryEscape("中文"), &privatePage)
	if len(privatePage.Items) != 1 || privatePage.Items[0].ID != private {
		t.Fatal("private tab search leaked a group", privatePage)
	}
	chatSearchAPIGet(t, h, "/api/v1/conversations?platform=weixin", &legacyPage)
	if len(legacyPage.Items) != 3 {
		t.Fatal("omitting kind changed legacy list behavior", legacyPage)
	}
	chatSearchAPIGet(t, h, "/api/v1/conversations?conversation_kind=group&archived=true", &archivePage)
	if len(archivePage.Items) != 1 || archivePage.Items[0].ID != archived {
		t.Fatal("kind did not intersect archive view", archivePage)
	}
}

func TestChatSearchAPIMessageLiteralQueriesAndScopedCursors(t *testing.T) {
	h := newEmailTestHandler(t)
	id := chatSearchAPIImport(t, h, "weixin", "account", "group", "group", `项目 中文 50% 字段_a O'Reilly "quoted"`, "ascii miXeD", "项目第二条", "最新无关正文")
	other := chatSearchAPIImport(t, h, "weixin", "another-account", "group", "private", `项目 中文 50% 字段_a O'Reilly "quoted"`)
	path := "/api/v1/conversations/" + id + "/messages"
	for _, fixture := range []struct{ q, externalID string }{
		{"中", "message-0"}, {"%", "message-0"}, {"_", "message-0"}, {"O'Reilly", "message-0"}, {`"quoted"`, "message-0"}, {"MIXED", "message-1"},
	} {
		var page store.MessagePage
		chatSearchAPIGet(t, h, path+"?q="+url.QueryEscape(fixture.q), &page)
		if len(page.Items) != 1 || page.Items[0].ConversationID != id || page.Items[0].ExternalID != fixture.externalID || page.NextCursor != nil {
			t.Fatal("literal message query leaked/skipped a result", fixture, page)
		}
	}
	for _, q := range []string{"*", "' OR 1=1 --", strings.Repeat("x", 256), strings.Repeat("中", 85) + "a"} {
		var page store.MessagePage
		chatSearchAPIGet(t, h, path+"?q="+url.QueryEscape(q), &page)
		if len(page.Items) != 0 || page.NextCursor != nil || page.Items == nil {
			t.Fatal("empty search result must be an empty bounded page", page)
		}
	}
	var first, second store.MessagePage
	chatSearchAPIGet(t, h, path+"?limit=1&q="+url.QueryEscape("项目"), &first)
	if len(first.Items) != 1 || first.Items[0].ExternalID != "message-2" || first.NextCursor == nil {
		t.Fatal("latest matching message was not selected before LIMIT", first)
	}
	cursor := url.QueryEscape(*first.NextCursor)
	chatSearchAPIGet(t, h, path+"?limit=1&q="+url.QueryEscape(" 项目 ")+"&cursor="+cursor, &second)
	if len(second.Items) != 1 || second.Items[0].ExternalID != "message-0" || second.NextCursor != nil {
		t.Fatal("older matching message continuation failed", second)
	}
	for _, changedPath := range []string{
		path + "?q=" + url.QueryEscape("第二条") + "&cursor=" + cursor,
		path + "?cursor=" + cursor,
		"/api/v1/conversations/" + other + "/messages?q=" + url.QueryEscape("项目") + "&cursor=" + cursor,
	} {
		response := emailTestRequest(h, http.MethodGet, changedPath, "", true)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "INVALID_CURSOR") {
			t.Fatal("message cursor escaped its query/conversation", response.Code, response.Body.String())
		}
	}
	legacy := emailTestRequest(h, http.MethodGet, path+"?limit=2", "", true)
	for _, q := range []string{"", " \t\u3000"} {
		response := emailTestRequest(h, http.MethodGet, path+"?limit=2&q="+url.QueryEscape(q), "", true)
		if response.Code != http.StatusOK || legacy.Code != http.StatusOK || response.Body.String() != legacy.Body.String() {
			t.Fatal("empty/default message query compatibility changed", response.Code, response.Body.String())
		}
	}
}

func TestChatSearchAPIRejectsInvalidQueriesAndWorkspaceClaims(t *testing.T) {
	h := newEmailTestHandler(t)
	id := chatSearchAPIImport(t, h, "weixin", "synthetic-account", "synthetic-peer", "group", "合成关键词")
	messages := "/api/v1/conversations/" + id + "/messages"
	for _, query := range []string{
		"conversation_kind=", "conversation_kind=all", "conversation_kind=GROUP", "conversation_kind=group%20", "conversation_kind=%FF",
		"conversation_kind=private&conversation_kind=group", "conversation_type=group",
	} {
		response := emailTestRequest(h, http.MethodGet, "/api/v1/conversations?"+query, "", true)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "INVALID_REQUEST") {
			t.Fatal("invalid conversation kind accepted", query, response.Code, response.Body.String())
		}
	}
	for _, query := range []string{"q=%FF", "q=%00", "q=a&q=b", "q=" + strings.Repeat("x", 257), "q=" + url.QueryEscape(strings.Repeat("中", 86))} {
		for _, path := range []string{"/api/v1/conversations", messages} {
			response := emailTestRequest(h, http.MethodGet, path+"?"+query, "", true)
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "INVALID_REQUEST") {
				t.Fatal("invalid keyword query accepted", path, response.Code, response.Body.String())
			}
		}
	}
	for _, query := range []string{"conversation_kind=group", "conversation_kind=private", "conversation_kind=", "platform=weixin", "category=client", "unread=true", "pinned=true", "archived=true"} {
		response := emailTestRequest(h, http.MethodGet, messages+"?"+query, "", true)
		if response.Code != http.StatusBadRequest {
			t.Fatal("message endpoint accepted a list-only filter", query, response.Code, response.Body.String())
		}
	}
	for _, path := range []string{"/api/v1/conversations?conversation_kind=group&q=" + url.QueryEscape("合成"), messages + "?q=" + url.QueryEscape("合成")} {
		if response := emailTestRequest(h, http.MethodGet, path, "", false); response.Code != http.StatusUnauthorized {
			t.Fatal("chat search became unauthenticated", response.Code)
		}
		if response := emailTestRequest(h, http.MethodGet, path+"&workspace_id=synthetic-other-workspace", "", true); response.Code != http.StatusForbidden {
			t.Fatal("query workspace claim selected another scope", response.Code, response.Body.String())
		}
		request := httptest.NewRequest(http.MethodGet, "http://"+config.DefaultAddr+path, nil)
		request.Header.Set("Authorization", "Bearer "+emailTestToken)
		request.Header.Set("Origin", "http://127.0.0.1:1420")
		request.Header.Set("X-Workspace-ID", "synthetic-other-workspace")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatal("header workspace claim selected another scope", response.Code, response.Body.String())
		}
	}
}
