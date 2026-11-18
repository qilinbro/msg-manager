package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/qilinbro/msg-manager/internal/agent"
	"github.com/qilinbro/msg-manager/internal/store"
)

func assistantChatSearchArguments(t *testing.T, fields map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func assistantChatSearchToolData(t *testing.T, input agent.ToolInput, callID, operationID string, data any) {
	t.Helper()
	if len(input.Messages) == 0 {
		t.Fatal("native provider received no tool result")
	}
	last := input.Messages[len(input.Messages)-1]
	var result assistantToolResult
	if last.Role != "tool" || last.ToolCallID != callID || json.Unmarshal([]byte(last.Content), &result) != nil || result.OperationID != operationID || result.State != "succeeded" || result.HTTPStatus == nil || *result.HTTPStatus != http.StatusOK || json.Unmarshal(result.Data, data) != nil {
		t.Fatal("genuine search tool result did not return to ChatTools", last)
	}
}

func TestAssistantChatSearchCatalogStrictQueries(t *testing.T) {
	const id = "synthetic-conversation"
	const q = `中文 50% 字段_a O'Reilly "quoted"`
	for _, name := range []string{"list_conversations", "get_conversation_messages"} {
		operation := assistantOperationByID(name)
		if operation == nil || operation.Effect != "read" || operation.Availability != "available" {
			t.Fatal("search changed read-only tool consent", name)
		}
		arguments := map[string]any{"q": q}
		if name == "list_conversations" {
			arguments["conversation_kind"], arguments["platform"] = "group", "weixin"
		} else {
			arguments["conversation_id"] = id
		}
		prepared, err := prepareAssistantOperation(operation, assistantChatSearchArguments(t, arguments), apiProfileConfig, 0)
		if err != nil {
			t.Fatal("valid native search arguments rejected", name, err)
		}
		query, err := url.ParseQuery(prepared.Query)
		if err != nil || query.Get("q") != q || query.Get("limit") != "20" || len(prepared.Body) != 0 {
			t.Fatal("query allowlist lost or changed literal text", prepared, err)
		}
		if name == "list_conversations" && (query.Get("conversation_kind") != "group" || query.Get("platform") != "weixin") || name == "get_conversation_messages" && (prepared.Path != "/api/v1/conversations/"+id+"/messages" || !slices.Equal(prepared.ConversationIDs, []string{id})) {
			t.Fatal("native query escaped its fixed path or filters", prepared)
		}
		for _, accepted := range []string{"", "   ", strings.Repeat("x", 256), strings.Repeat("中", 85) + "a"} {
			arguments["q"] = accepted
			if _, err := prepareAssistantOperation(operation, assistantChatSearchArguments(t, arguments), apiProfileConfig, 0); err != nil {
				t.Fatal("valid UTF-8 byte-boundary search rejected", name, err)
			}
		}
		for _, rejected := range []any{strings.Repeat("x", 257), strings.Repeat("中", 86), "query\x00tail", nil, true} {
			arguments["q"] = rejected
			if _, err := prepareAssistantOperation(operation, assistantChatSearchArguments(t, arguments), apiProfileConfig, 0); err == nil {
				t.Fatal("invalid native keyword accepted", name)
			}
		}
	}
	for _, raw := range []string{`{"conversation_kind":""}`, `{"conversation_kind":"all"}`, `{"conversation_kind":"GROUP"}`, `{"conversation_kind":"group "}`, `{"conversation_kind":null}`, `{"conversation_kind":"private","conversation_kind":"group"}`, `{"conversation_kind":"group","workspace_id":"other"}`} {
		if _, err := prepareAssistantOperation(assistantOperationByID("list_conversations"), raw, apiProfileConfig, 0); err == nil {
			t.Fatal("native list accepted an unrestricted kind/workspace", raw)
		}
	}
	for _, extra := range []string{`"conversation_kind":"group"`, `"workspace_id":"other"`, `"url":"https://example.test"`, `"q":"a","q":"b"`} {
		if _, err := prepareAssistantOperation(assistantOperationByID("get_conversation_messages"), `{"conversation_id":"`+id+`",`+extra+`}`, apiProfileConfig, 0); err == nil {
			t.Fatal("native message search accepted a list-only or injected field", extra)
		}
	}
}

func TestAssistantChatSearchNativeGroupQueryReadsOlderMatchesWithoutPrivacyLeaks(t *testing.T) {
	h, provider, _, _ := newAssistantFixture(t)
	const q = `项目 50% 字段_a O'Reilly "quoted"`
	const pausedMarker = "SYNTHETIC_PAUSED_SEARCH_BODY_NEVER_OUTBOUND"
	const privateMarker = "SYNTHETIC_PRIVATE_SEARCH_BODY_NEVER_OUTBOUND"
	const otherMarker = "SYNTHETIC_OTHER_ACCOUNT_SEARCH_BODY_NEVER_OUTBOUND"
	const platformMarker = "SYNTHETIC_OTHER_PLATFORM_SEARCH_BODY_NEVER_OUTBOUND"
	const nonmatchMarker = "SYNTHETIC_NONMATCH_SEARCH_BODY_NEVER_OUTBOUND"
	target := chatSearchAPIImport(t, h, "weixin", "search-account", "group", "group", q+" 旧命中", "50Z 字段Xa 无关正文", q+" 新命中", "最新无关预览")
	paused := chatSearchAPIImport(t, h, "weixin", "search-account", "paused", "group", q+" "+pausedMarker)
	chatSearchAPIImport(t, h, "weixin", "search-account", "private", "private", q+" "+privateMarker)
	other := chatSearchAPIImport(t, h, "weixin", "other-account", "group", "group", q+" "+otherMarker)
	chatSearchAPIImport(t, h, "qq", "search-account", "group", "group", q+" "+platformMarker)
	chatSearchAPIImport(t, h, "weixin", "search-account", "nonmatch", "group", nonmatchMarker)
	pin, archive := true, true
	if err := h.inbox.UpdateConversation(paused, store.ConversationPatch{Pinned: &pin}); err != nil {
		t.Fatal(err)
	}
	if err := h.inbox.UpdateConversation(other, store.ConversationPatch{Archived: &archive}); err != nil {
		t.Fatal(err)
	}
	if err := h.inbox.UpdateAISettings(paused, "paused", "natural"); err != nil {
		t.Fatal(err)
	}
	humanPage, err := h.inbox.Conversations(store.Filter{Q: q, Platform: "weixin", ConversationKind: "group", Limit: 1})
	if err != nil || len(humanPage.Items) != 1 || humanPage.Items[0].ID != paused || humanPage.NextCursor == nil || !strings.Contains(humanPage.Items[0].MatchedMessage, pausedMarker) {
		t.Fatal("missing synthetic paused first-page/excerpt fixture", humanPage, err)
	}
	listArguments := func(cursor string) string {
		fields := map[string]any{"q": q, "platform": "weixin", "conversation_kind": "group", "limit": 1}
		if cursor != "" {
			fields["cursor"] = cursor
		}
		return assistantChatSearchArguments(t, fields)
	}
	messageArguments := func(cursor string) string {
		fields := map[string]any{"conversation_id": target, "q": q, "limit": 1}
		if cursor != "" {
			fields["cursor"] = cursor
		}
		return assistantChatSearchArguments(t, fields)
	}
	provider.run = func(_ context.Context, index int, input agent.ToolInput) (agent.ToolResult, error) {
		encoded, _ := json.Marshal(input)
		for _, marker := range []string{pausedMarker, privateMarker, otherMarker, platformMarker, nonmatchMarker} {
			if strings.Contains(string(encoded), marker) {
				t.Fatal("native search leaked a paused/nonmatching/other-conversation body or excerpt", marker)
			}
		}
		switch index {
		case 1:
			published := 0
			for _, tool := range input.Tools {
				if tool.Name != "list_conversations" && tool.Name != "get_conversation_messages" {
					continue
				}
				var schema struct {
					Properties map[string]struct {
						MaxLength int      `json:"maxLength"`
						Enum      []string `json:"enum"`
					} `json:"properties"`
					Required []string `json:"required"`
				}
				if json.Unmarshal(tool.Parameters, &schema) != nil || schema.Properties["q"].MaxLength != 256 || slices.Contains(schema.Required, "q") {
					t.Fatal("ChatTools did not receive the optional bounded q schema", tool)
				}
				if tool.Name == "list_conversations" && (!slices.Equal(schema.Properties["conversation_kind"].Enum, []string{"private", "group"}) || slices.Contains(schema.Required, "conversation_kind")) {
					t.Fatal("ChatTools did not receive the optional strict kind enum", tool)
				}
				published++
			}
			if published != 2 {
				t.Fatal("native search tools missing from published catalog")
			}
			return assistantFixtureCall("search_groups_first", "list_conversations", listArguments("")), nil
		case 2, 3:
			var page struct {
				store.ConversationPage
				Privacy struct {
					Filtered int `json:"filtered_paused"`
				} `json:"assistant_privacy"`
			}
			callID := "search_groups_first"
			if index == 3 {
				callID = "search_groups_next"
			}
			assistantChatSearchToolData(t, input, callID, "list_conversations", &page)
			if index == 2 {
				if len(page.Items) != 0 || page.Privacy.Filtered != 1 || page.NextCursor == nil || *page.NextCursor != *humanPage.NextCursor {
					t.Fatal("paused search page lost its cursor or leaked its excerpt", page)
				}
				return assistantFixtureCall("search_groups_next", "list_conversations", listArguments(*page.NextCursor)), nil
			}
			if len(page.Items) != 1 || page.Items[0].ID != target || page.Items[0].ConversationKind != "group" || page.Items[0].Platform != "weixin" || page.Items[0].MatchedMessage != q+" 新命中" || page.Items[0].LastMessage != "最新无关预览" || page.NextCursor != nil || page.Privacy.Filtered != 0 {
				t.Fatal("native list did not intersect kind/platform/q with real bounded body evidence", page)
			}
			return assistantFixtureCall("search_messages_latest", "get_conversation_messages", messageArguments("")), nil
		case 4, 5:
			var page store.MessagePage
			callID, externalID := "search_messages_latest", "message-2"
			if index == 5 {
				callID, externalID = "search_messages_older", "message-0"
			}
			assistantChatSearchToolData(t, input, callID, "get_conversation_messages", &page)
			if len(page.Items) != 1 || page.Items[0].ConversationID != target || page.Items[0].ExternalID != externalID || !strings.Contains(page.Items[0].Body, q) || (page.NextCursor == nil) != (index == 5) {
				t.Fatal("native message search failed to return the older matching body", page)
			}
			if index == 4 {
				return assistantFixtureCall("search_messages_older", "get_conversation_messages", messageArguments(*page.NextCursor)), nil
			}
			return assistantFixtureAnswer("已使用真实只读工具读取未暂停群聊中更旧的合成正文命中。"), nil
		default:
			t.Fatal("unexpected native search iteration", index)
			return agent.ToolResult{}, nil
		}
	}
	response := assistantFixtureStart(t, h, "")
	if response.State != "completed" || response.PendingAction != nil || provider.calls.Load() != 5 || len(response.ToolResults) != 4 {
		t.Fatal("genuine ChatTools search/list/message continuation failed", response)
	}
}

func TestAssistantChatSearchPausedQueryAndCrossConversationCursorFailClosed(t *testing.T) {
	t.Run("paused body remains human-only", func(t *testing.T) {
		h, provider, _, _ := newAssistantFixture(t)
		const marker = "SYNTHETIC_PAUSED_KEYWORD_BODY_NEVER_OUTBOUND"
		id := chatSearchAPIImport(t, h, "weixin", "search-account", "paused", "group", "待检索 "+marker)
		if err := h.inbox.UpdateAISettings(id, "paused", "natural"); err != nil {
			t.Fatal(err)
		}
		human := emailTestRequest(h, http.MethodGet, "/api/v1/conversations/"+id+"/messages?q="+url.QueryEscape("待检索"), "", true)
		if human.Code != http.StatusOK || !strings.Contains(human.Body.String(), marker) {
			t.Fatal("assistant privacy changed authenticated human keyword reads")
		}
		provider.run = func(_ context.Context, index int, input agent.ToolInput) (agent.ToolResult, error) {
			encoded, _ := json.Marshal(input)
			if index != 1 || strings.Contains(string(encoded), marker) {
				t.Fatal("paused keyword body reached a later native model call")
			}
			return assistantFixtureCall("paused_keyword_read", "get_conversation_messages", assistantChatSearchArguments(t, map[string]any{"conversation_id": id, "q": "待检索"})), nil
		}
		response := assistantFixtureStart(t, h, "")
		if response.State != "failed" || response.Error == nil || response.Error.Code != "ASSISTANT_AI_PAUSED" || provider.calls.Load() != 1 || len(response.ToolResults) != 1 || string(response.ToolResults[0].Data) != "null" {
			t.Fatal("paused query escaped existing assistant privacy barriers", response)
		}
	})
	t.Run("filtered cursor cannot select another conversation", func(t *testing.T) {
		h, provider, _, _ := newAssistantFixture(t)
		const firstMarker, otherMarker = "SYNTHETIC_CURSOR_FIRST_BODY", "SYNTHETIC_CURSOR_OTHER_BODY"
		first := chatSearchAPIImport(t, h, "weixin", "first-account", "same-peer", "group", "关键词 "+firstMarker+" 旧", "关键词 "+firstMarker+" 新")
		other := chatSearchAPIImport(t, h, "weixin", "other-account", "same-peer", "group", "关键词 "+otherMarker)
		page, err := h.inbox.SearchMessages(first, "关键词", "", 1)
		if err != nil || page.NextCursor == nil {
			t.Fatal("missing synthetic filtered cursor", err)
		}
		provider.run = func(_ context.Context, index int, input agent.ToolInput) (agent.ToolResult, error) {
			encoded, _ := json.Marshal(input)
			if strings.Contains(string(encoded), firstMarker) || strings.Contains(string(encoded), otherMarker) {
				t.Fatal("cross-conversation cursor exposed a raw body")
			}
			if index == 1 {
				return assistantFixtureCall("cross_conversation_search", "get_conversation_messages", assistantChatSearchArguments(t, map[string]any{"conversation_id": other, "q": "关键词", "cursor": *page.NextCursor})), nil
			}
			last := input.Messages[len(input.Messages)-1]
			var result assistantToolResult
			if index != 2 || last.Role != "tool" || json.Unmarshal([]byte(last.Content), &result) != nil || result.State != "failed" || result.ErrorCode != "ASSISTANT_TOOL_REJECTED" || result.HTTPStatus == nil || *result.HTTPStatus != http.StatusBadRequest || string(result.Data) != "null" {
				t.Fatal("invalid search cursor was not rejected by the actual business handler", last)
			}
			return assistantFixtureAnswer("游标范围不一致，未读取任何正文。"), nil
		}
		response := assistantFixtureStart(t, h, "")
		if response.State != "completed" || provider.calls.Load() != 2 || len(response.ToolResults) != 1 || response.ToolResults[0].State != "failed" {
			t.Fatal("native cursor-scope rejection did not fail closed", response)
		}
	})
}

func TestAssistantChatSearchReadConsentDoesNotAuthorizeMutation(t *testing.T) {
	h, provider, first, _ := newAssistantFixture(t)
	conversation := func() store.Conversation {
		page, err := h.inbox.Conversations(store.Filter{})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if item.ID == first {
				return item
			}
		}
		t.Fatal("synthetic conversation missing")
		return store.Conversation{}
	}
	provider.run = func(_ context.Context, index int, input agent.ToolInput) (agent.ToolResult, error) {
		switch index {
		case 1:
			return assistantFixtureCall("search_before_mutation", "get_conversation_messages", assistantChatSearchArguments(t, map[string]any{"conversation_id": first, "q": "合成"})), nil
		case 2:
			var page store.MessagePage
			assistantChatSearchToolData(t, input, "search_before_mutation", "get_conversation_messages", &page)
			if len(page.Items) != 1 || page.Items[0].ConversationID != first {
				t.Fatal("synthetic pre-mutation query failed", page)
			}
			return assistantFixtureCall("pin_after_search", "update_conversation", assistantChatSearchArguments(t, map[string]any{"conversation_id": first, "pinned": true})), nil
		case 3:
			var receipt struct {
				OK bool `json:"ok"`
			}
			assistantChatSearchToolData(t, input, "pin_after_search", "update_conversation", &receipt)
			if !receipt.OK {
				t.Fatal("explicitly confirmed mutation did not return its real result")
			}
			return assistantFixtureAnswer("仅在逐次人工确认后完成一次合成置顶。"), nil
		default:
			t.Fatal("unexpected native mutation iteration", index)
			return agent.ToolResult{}, nil
		}
	}
	unconfirmed := strings.Replace(assistantFixtureStartBody("", "仅检索合成资料", 0), `"confirmed":true`, `"confirmed":false`, 1)
	if response := emailTestRequest(h, http.MethodPost, assistantPrefix+"/runs", unconfirmed, true); response.Code != http.StatusBadRequest || provider.calls.Load() != 0 {
		t.Fatal("native keyword search bypassed initial model consent", response.Code)
	}
	pending := assistantFixtureStart(t, h, "")
	if pending.State != "awaiting_confirmation" || pending.PendingAction == nil || pending.PendingAction.OperationID != "update_conversation" || len(pending.ToolResults) != 2 || pending.ToolResults[1].State != "pending" || provider.calls.Load() != 2 || conversation().Pinned {
		t.Fatal("keyword read consent executed an unconfirmed mutation", pending)
	}
	path := assistantPrefix + "/runs/" + pending.RunID + "/confirm"
	body := assistantFixtureResolveBody(pending, "confirm")
	for _, rejected := range []string{strings.Replace(body, `"confirmed":true`, `"confirmed":false`, 1), strings.TrimSuffix(body, "}") + `,"q":"replacement"}`} {
		if response := emailTestRequest(h, http.MethodPost, path, rejected, true); response.Code != http.StatusBadRequest || conversation().Pinned || provider.calls.Load() != 2 {
			t.Fatal("mutation accepted missing/extended per-action consent", response.Code, response.Body.String())
		}
	}
	final := assistantFixtureResponse(t, emailTestRequest(h, http.MethodPost, path, body, true))
	if final.State != "completed" || final.PendingAction != nil || len(final.ToolResults) != 2 || provider.calls.Load() != 3 || !conversation().Pinned {
		t.Fatal("confirmed search-following mutation did not execute exactly once", final)
	}
	if replay := emailTestRequest(h, http.MethodPost, path, body, true); replay.Code != http.StatusConflict || provider.calls.Load() != 3 {
		t.Fatal("search-following mutation confirmation was reusable", replay.Code)
	}
}
