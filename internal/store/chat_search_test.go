package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

func chatSearchConversation(t *testing.T, s *Store, platform, account, peer, title, kind string, bodies ...string) string {
	t.Helper()
	request := ImportRequest{Platform: platform, AccountID: account, AccountName: "别名" + account}
	for index, body := range bodies {
		request.Messages = append(request.Messages, ImportMessage{
			ExternalID: fmt.Sprintf("m-%02d", index), PeerID: peer, PeerName: title, ConversationKind: kind,
			Direction: "incoming", Body: body, OccurredAt: fmt.Sprintf("2026-01-01T00:00:%02dZ", index/3),
		})
	}
	result := mustImport(t, s, request)
	if len(result.ConversationIDs) != 1 {
		t.Fatal("synthetic conversation import did not return one ID", result)
	}
	return result.ConversationIDs[0]
}

func chatSearchWantConversations(t *testing.T, page ConversationPage, want ...string) {
	t.Helper()
	got := make([]string, 0, len(page.Items))
	for _, item := range page.Items {
		got = append(got, item.ID)
	}
	slices.Sort(got)
	want = slices.Clone(want)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("conversation search IDs = %v, want %v", got, want)
	}
}

func TestChatSearchConversationKindIntersectsViewsAndPagination(t *testing.T) {
	s := testStore(t)
	fixtures := []struct {
		peer, platform, kind, category string
		pinned, archived, read, noHit  bool
	}{
		{"group-a", "weixin", "group", "client", true, false, false, false},
		{"group-b", "weixin", "group", "client", true, false, false, false},
		{"private", "weixin", "private", "client", true, false, false, false},
		{"other-platform", "qq", "group", "client", true, false, false, false},
		{"archived", "weixin", "group", "client", true, true, false, false},
		{"read", "weixin", "group", "client", true, false, true, false},
		{"unpinned", "weixin", "group", "client", false, false, false, false},
		{"other-category", "weixin", "group", "family", true, false, false, false},
		{"no-hit", "weixin", "group", "client", true, false, false, true},
	}
	ids := map[string]string{}
	for _, fixture := range fixtures {
		body := "历史正文：中文项目 50% 字段_a O'Reilly"
		if fixture.noHit {
			body = "没有关键词的正文"
		}
		id := chatSearchConversation(t, s, fixture.platform, "synthetic-account", fixture.peer, "合成会话", fixture.kind, body, "最新预览")
		ids[fixture.peer] = id
		if err := s.UpdateConversation(id, ConversationPatch{Pinned: boolValue(fixture.pinned), Archived: boolValue(fixture.archived), Read: boolValue(fixture.read)}); err != nil {
			t.Fatal(err)
		}
		patchProfile(t, s, id, ProfilePatch{ExpectedRevision: profilePointer(int64(0)), CategoryOverride: profilePointer(fixture.category)})
	}
	chatSearchWantConversations(t, mustConversations(t, s, Filter{ConversationKind: "private"}), ids["private"])
	chatSearchWantConversations(t, mustConversations(t, s, Filter{ConversationKind: "group", Archived: true}), ids["archived"])
	if len(mustConversations(t, s, Filter{}).Items) != 8 || len(mustConversations(t, s, Filter{ConversationKind: "group"}).Items) != 7 {
		t.Fatal("omitted kind or default archive behavior changed")
	}

	filter := Filter{ConversationKind: "group", Platform: "weixin", Q: "中文", Category: "client", Unread: true, Pinned: true, Limit: 1}
	first := mustConversations(t, s, filter)
	if len(first.Items) != 1 || first.NextCursor == nil || first.Items[0].LastMessage != "最新预览" {
		t.Fatalf("search must filter before LIMIT while preserving latest preview: %+v", first)
	}
	filter.Cursor = *first.NextCursor
	second := mustConversations(t, s, filter)
	if len(second.Items) != 1 || second.NextCursor != nil || first.Items[0].ID == second.Items[0].ID {
		t.Fatalf("kind search page duplicated or skipped results: %+v %+v", first, second)
	}
	chatSearchWantConversations(t, ConversationPage{Items: append(slices.Clone(first.Items), second.Items...)}, ids["group-a"], ids["group-b"])
	for _, mutate := range []func(*Filter){
		func(f *Filter) { f.ConversationKind = "private" },
		func(f *Filter) { f.ConversationKind = "" },
		func(f *Filter) { f.Platform = "qq" },
		func(f *Filter) { f.Q = "项目" },
		func(f *Filter) { f.Category = "family" },
		func(f *Filter) { f.Archived = true },
		func(f *Filter) { f.Unread = false },
		func(f *Filter) { f.Pinned = false },
	} {
		changed := filter
		mutate(&changed)
		if _, err := s.Conversations(changed); !errors.Is(err, ErrInvalidCursor) {
			t.Fatal("conversation cursor accepted changed filters", changed, err)
		}
	}
	for _, kind := range []string{"all", "Group", " private", "group ", "%", string([]byte{0xff})} {
		if _, err := s.Conversations(Filter{ConversationKind: kind}); !errors.Is(err, ErrInvalidInput) {
			t.Fatal("invalid kind accepted", kind, err)
		}
	}
}

func TestChatSearchConversationPreservesLiteralAndBroadQuerySemantics(t *testing.T) {
	s := testStore(t)
	id := chatSearchConversation(t, s, "weixin", "account-search", "peer-lookup", "中文名字", "private", `历史50% 字段_a O'Reilly "quoted"`, "最新预览")
	chatSearchConversation(t, s, "weixin", "unrelated", "other-peer", "无关好友", "group", "50Z 字段Xa OXReilly quoted")
	for _, q := range []string{"中", "名字", "peer-lookup", "account-search", "别名account-search", "%", "_", "O'Reilly", `"quoted"`, "  历史  "} {
		page := mustConversations(t, s, Filter{Q: q, ConversationKind: "private"})
		chatSearchWantConversations(t, page, id)
		if page.Items[0].LastMessage != "最新预览" {
			t.Fatal("a historical keyword match replaced the latest preview")
		}
		wantMatch := ""
		if strings.Contains(`历史50% 字段_a O'Reilly "quoted"`, strings.TrimSpace(q)) {
			wantMatch = `历史50% 字段_a O'Reilly "quoted"`
		}
		if page.Items[0].MatchedMessage != wantMatch {
			t.Fatal("body evidence was missing, fabricated, or taken from another match", q, page.Items[0].MatchedMessage)
		}
	}
	for _, q := range []string{"*", "' OR 1=1 --", "不存在"} {
		chatSearchWantConversations(t, mustConversations(t, s, Filter{Q: q}))
	}
	mail := mustImport(t, s, ImportRequest{Platform: "email", AccountID: "synthetic-mail", Messages: []ImportMessage{{
		ExternalID: "mail", PeerID: "sender@example.test", PeerName: "合成邮件", Direction: "incoming", Body: "中性正文", OccurredAt: "2026-01-01T00:00:00Z",
		Email: &EmailHeaders{Subject: "邮件头专用关键词", FromAddress: "sender@example.test", To: []string{"receiver@example.test"}, Folder: "INBOX"},
	}}})
	mailPage := mustConversations(t, s, Filter{Q: "邮件头专用", ConversationKind: "private"})
	chatSearchWantConversations(t, mailPage, mail.ConversationIDs[0])
	if mailPage.Items[0].MatchedMessage != "" {
		t.Fatal("an email-header-only match fabricated a message-body excerpt")
	}
	page, err := s.SearchMessages(mail.ConversationIDs[0], "邮件头专用", "", 50)
	if err != nil || len(page.Items) != 0 {
		t.Fatal("message-body search incorrectly matched an email header", err, page)
	}

	// Unfiltered callers retain the pre-kind fingerprint and default results.
	first := mustConversations(t, s, Filter{Limit: 1})
	var revision int64
	if err := s.db.QueryRow(`SELECT revision FROM workspaces WHERE id=?`, workspaceID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if first.NextCursor == nil {
		t.Fatal("missing compatibility cursor fixture")
	}
	if _, err := decodeCursor(*first.NextCursor, fingerprint("conversations", workspaceID, "", "", false, false, false, ""), revision); err != nil {
		t.Fatal("omitting kind changed the legacy cursor fingerprint", err)
	}
}

func TestChatSearchConversationMatchedMessageIsBoundedOriginalText(t *testing.T) {
	s := testStore(t)
	q := `项目%_ O'Reilly "原文"`
	body := strings.Repeat("前", 70) + q + strings.Repeat("后", 500)
	id := chatSearchConversation(t, s, "weixin", "account", "group", "名称专用关键词", "group", "更早"+q, body, "最新无关预览")
	chatSearchConversation(t, s, "weixin", "other-account", "same-name", "名称专用关键词", "private", "他人正文"+q)
	for _, query := range []string{q, "  " + q + "  "} {
		page := mustConversations(t, s, Filter{Q: query, ConversationKind: "group"})
		chatSearchWantConversations(t, page, id)
		want := strings.Repeat("前", 40) + q + strings.Repeat("后", 80)
		if page.Items[0].MatchedMessage != want || !strings.Contains(body, page.Items[0].MatchedMessage) || page.Items[0].LastMessage != "最新无关预览" {
			t.Fatal("excerpt is not the bounded original text around the latest historical match", page.Items[0])
		}
		if utf8.RuneCountInString(page.Items[0].MatchedMessage) != utf8.RuneCountInString(q)+120 {
			t.Fatal("excerpt bounds counted UTF-8 bytes instead of characters")
		}
	}
	maxQ := strings.Repeat("K", 256)
	maxBody := strings.Repeat("界", 70) + maxQ + strings.Repeat("尾", 500)
	maxID := chatSearchConversation(t, s, "weixin", "account", "max-group", "最大边界", "group", maxBody, "最新无关预览")
	maxPage := mustConversations(t, s, Filter{Q: strings.ToLower(maxQ), ConversationKind: "group"})
	chatSearchWantConversations(t, maxPage, maxID)
	if excerpt := maxPage.Items[0].MatchedMessage; utf8.RuneCountInString(excerpt) != 376 || excerpt != strings.Repeat("界", 40)+maxQ+strings.Repeat("尾", 80) {
		t.Fatal("maximum excerpt bound or original ASCII case changed", excerpt)
	}
	for _, query := range []string{"", " \u3000", "名称专用"} {
		page := mustConversations(t, s, Filter{Q: query, ConversationKind: "group"})
		if len(page.Items) == 0 {
			t.Fatal("missing metadata/default excerpt fixture")
		}
		for _, item := range page.Items {
			encoded, err := json.Marshal(item)
			if err != nil || item.MatchedMessage != "" || strings.Contains(string(encoded), `"matched_message"`) {
				t.Fatal("default/metadata-only match exposed or fabricated a body excerpt", item, err)
			}
		}
	}
}

func TestChatSearchMessagesLiteralUnicodeAndDefaultCompatibility(t *testing.T) {
	s := testStore(t)
	id := chatSearchConversation(t, s, "weixin", "same-account", "same-peer", "同名好友", "private",
		`中文项目 50% 字段_a O'Reilly "quoted"`, "ascii miXeD", "50Z 字段Xa OXReilly quoted", "最新无关正文")
	chatSearchConversation(t, s, "weixin", "same-account", "other-peer", "同名好友", "private", `中文项目 50% 字段_a O'Reilly "quoted"`)
	chatSearchConversation(t, s, "weixin", "other-account", "same-peer", "同名好友", "group", `中文项目 50% 字段_a O'Reilly "quoted"`)
	for _, fixture := range []struct{ q, externalID string }{
		{"中", "m-00"}, {"中文项目", "m-00"}, {"%", "m-00"}, {"_", "m-00"}, {"O'Reilly", "m-00"}, {`"quoted"`, "m-00"}, {"MIXED", "m-01"}, {"  项目  ", "m-00"},
	} {
		page, err := s.SearchMessages(id, fixture.q, "", 50)
		if err != nil || len(page.Items) != 1 || page.Items[0].ConversationID != id || page.Items[0].ExternalID != fixture.externalID || page.NextCursor != nil {
			t.Fatalf("literal/scoped message search %q failed: %+v %v", fixture.q, page, err)
		}
	}
	for _, q := range []string{"*", "' OR 1=1 --", "不存在", strings.Repeat("x", 256), strings.Repeat("中", 85) + "a"} {
		page, err := s.SearchMessages(id, q, "", 50)
		if err != nil || len(page.Items) != 0 || page.NextCursor != nil {
			t.Fatal("literal no-match query broadened results", q, page, err)
		}
	}
	for _, q := range []string{strings.Repeat("x", 257), strings.Repeat("中", 86), "text\x00tail", string([]byte{0xff})} {
		if _, err := s.SearchMessages(id, q, "", 50); !errors.Is(err, ErrInvalidInput) {
			t.Fatal("invalid message query accepted", err)
		}
	}
	original, err := s.Messages(id, "", 2)
	if err != nil || original.NextCursor == nil {
		t.Fatal("missing default page fixture", err)
	}
	for _, q := range []string{"", " \t\u3000"} {
		page, err := s.SearchMessages(id, q, "", 2)
		if err != nil || !reflect.DeepEqual(page, original) {
			t.Fatal("empty query changed the original message page", page, original, err)
		}
		next, err := s.SearchMessages(id, q, *original.NextCursor, 2)
		legacyNext, legacyErr := s.Messages(id, *original.NextCursor, 2)
		if err != nil || legacyErr != nil || !reflect.DeepEqual(next, legacyNext) {
			t.Fatal("empty query broke legacy cursor continuation", err, legacyErr)
		}
	}
}

func TestChatSearchMessagesStableFilteredCursorPaging(t *testing.T) {
	s := testStore(t)
	bodies := make([]string, 12)
	for index := range bodies {
		bodies[index] = "无关正文"
		if index%2 == 0 {
			bodies[index] = "项目命中"
		}
	}
	id := chatSearchConversation(t, s, "weixin", "account", "peer", "同名会话", "group", bodies...)
	other := chatSearchConversation(t, s, "weixin", "account", "other-peer", "同名会话", "group", bodies...)
	cursor, firstCursor := "", ""
	for index, want := range [][]string{{"m-08", "m-10"}, {"m-04", "m-06"}, {"m-00", "m-02"}} {
		page, err := s.SearchMessages(id, "项目", cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		got := []string{}
		for _, item := range page.Items {
			got = append(got, item.ExternalID)
			if item.ConversationID != id || !strings.Contains(item.Body, "项目") {
				t.Fatal("message search crossed the conversation or keyword boundary", item)
			}
		}
		if !slices.Equal(got, want) || (page.NextCursor == nil) != (index == 2) {
			t.Fatalf("filtered page %d = %v, want %v; cursor=%v", index, got, want, page.NextCursor)
		}
		if page.NextCursor != nil {
			cursor = *page.NextCursor
			if firstCursor == "" {
				firstCursor = cursor
			}
		}
	}
	for _, fixture := range []struct{ conversation, q string }{{id, "命中"}, {id, ""}, {other, "项目"}} {
		if _, err := s.SearchMessages(fixture.conversation, fixture.q, firstCursor, 2); !errors.Is(err, ErrInvalidCursor) {
			t.Fatal("message cursor accepted another query/conversation", fixture, err)
		}
	}
	if _, err := s.SearchMessages(id, " 项目 ", firstCursor, 2); err != nil {
		t.Fatal("equivalent trimmed query rejected its cursor", err)
	}
	legacy, err := s.Messages(id, "", 2)
	if err != nil || legacy.NextCursor == nil {
		t.Fatal("missing unfiltered cursor fixture", err)
	}
	if _, err := s.SearchMessages(id, "项目", *legacy.NextCursor, 2); !errors.Is(err, ErrInvalidCursor) {
		t.Fatal("search accepted an unfiltered cursor", err)
	}
	var revision int64
	if err := s.db.QueryRow(`SELECT revision FROM workspaces WHERE id=?`, workspaceID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeCursor(*legacy.NextCursor, fingerprint("messages", workspaceID, id), revision); err != nil {
		t.Fatal("empty query changed the legacy message cursor fingerprint", err)
	}
	position, err := decodeCursor(firstCursor, fingerprint("messages", workspaceID, id, "项目"), revision)
	if err != nil {
		t.Fatal(err)
	}
	position.Filter = fingerprint("messages", "synthetic-foreign-workspace", id, "项目")
	if _, err := s.SearchMessages(id, "项目", encodeCursor(position), 2); !errors.Is(err, ErrInvalidCursor) {
		t.Fatal("cursor from another workspace accepted", err)
	}
	if err := s.UpdateConversation(id, ConversationPatch{Pinned: boolValue(true)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SearchMessages(id, "项目", firstCursor, 2); !errors.Is(err, ErrInvalidCursor) {
		t.Fatal("search cursor ignored workspace revision", err)
	}
}

func TestChatSearchWorkspaceIsolationEvenWithForeignRows(t *testing.T) {
	s := testStore(t)
	local := chatSearchConversation(t, s, "weixin", "local-account", "group", "本地群", "group", "本地可见")
	other := chatSearchConversation(t, s, "weixin", "local-account", "private", "本地好友", "private", "foreign-only 其他会话")
	foreignConnection, foreignContact, foreignConversation := stableID("search-foreign-connection"), stableID("search-foreign-contact"), stableID("search-foreign-conversation")
	const foreignWorkspace = "synthetic-foreign-workspace"
	// This encrypted temporary database alone simulates foreign/damaged rows.
	// Read predicates must still scope every lookup if schema guards are bypassed.
	if _, err := s.db.Exec(`PRAGMA ignore_check_constraints=ON`); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO workspaces(id) VALUES(?)`, []any{foreignWorkspace}},
		{`INSERT INTO connections(id,workspace_id,platform,account_id,display_name,source) VALUES(?,?,'weixin','foreign-account','foreign-only','import')`, []any{foreignConnection, foreignWorkspace}},
		{`INSERT INTO contacts(id,workspace_id,connection_id,peer_id,display_name) VALUES(?,?,?,'foreign-peer','foreign-only')`, []any{foreignContact, foreignWorkspace, foreignConnection}},
		{`INSERT INTO conversations(id,workspace_id,connection_id,contact_id,peer_id,title,conversation_kind) VALUES(?,?,?,?,'foreign-peer','foreign-only','group')`, []any{foreignConversation, foreignWorkspace, foreignConnection, foreignContact}},
		{`INSERT INTO messages(id,workspace_id,conversation_id,external_id,direction,body,occurred_at,is_read) VALUES(?,?,?,'foreign','incoming','foreign-only','2026-01-01T00:10:00.000000000Z',0)`, []any{stableID("search-foreign-message"), foreignWorkspace, foreignConversation}},
		{`INSERT INTO messages(id,workspace_id,conversation_id,external_id,direction,body,occurred_at,is_read) VALUES(?,?,?,'foreign-shadow','incoming','foreign-only 本地','2026-01-01T00:20:00.000000000Z',0)`, []any{stableID("search-foreign-shadow"), foreignWorkspace, local}},
	} {
		if _, err := s.db.Exec(statement.query, statement.args...); err != nil {
			t.Fatal("foreign-row synthetic fixture", err)
		}
	}
	if _, err := s.db.Exec(`PRAGMA ignore_check_constraints=OFF`); err != nil {
		t.Fatal(err)
	}
	chatSearchWantConversations(t, mustConversations(t, s, Filter{Q: "foreign-only", ConversationKind: "group"}))
	chatSearchWantConversations(t, mustConversations(t, s, Filter{Q: "foreign-only"}), other)
	localMatch := mustConversations(t, s, Filter{Q: "本地", ConversationKind: "group"})
	chatSearchWantConversations(t, localMatch, local)
	if localMatch.Items[0].MatchedMessage != "本地可见" {
		t.Fatal("body excerpt crossed the workspace boundary", localMatch)
	}
	page := mustConversations(t, s, Filter{ConversationKind: "group"})
	chatSearchWantConversations(t, page, local)
	if page.Items[0].LastMessage != "本地可见" || page.Items[0].UnreadCount != 1 {
		t.Fatal("foreign-workspace message changed local preview/unread", page)
	}
	messages, err := s.SearchMessages(local, "foreign-only", "", 50)
	if err != nil || len(messages.Items) != 0 {
		t.Fatal("message search crossed workspace/conversation boundary", messages, err)
	}
	messages, err = s.Messages(local, "", 50)
	if err != nil || len(messages.Items) != 1 || messages.Items[0].Body != "本地可见" {
		t.Fatal("unfiltered message page crossed workspace boundary", messages, err)
	}
	if _, err := s.SearchMessages(foreignConversation, "foreign-only", "", 50); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign-workspace conversation was discoverable", err)
	}
}
