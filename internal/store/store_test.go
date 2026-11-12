package store

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "messages.db"), testKey)
	if err != nil {
		t.Fatalf("open encrypted test store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func importMessage(external, peer, body, at string) ImportMessage {
	return ImportMessage{ExternalID: external, PeerID: peer, PeerName: "同名好友", Direction: "incoming", Body: body, OccurredAt: at}
}

func mustImport(t *testing.T, s *Store, req ImportRequest) ImportResult {
	t.Helper()
	result, err := s.Import(req)
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}
	return result
}

func mustConversations(t *testing.T, s *Store, filter Filter) ConversationPage {
	t.Helper()
	page, err := s.Conversations(filter)
	if err != nil {
		t.Fatalf("list conversations failed: %v", err)
	}
	return page
}

func boolValue(value bool) *bool { return &value }

func TestImportSeparatesPlatformAccountAndPeerIdentity(t *testing.T) {
	s := testStore(t)
	for _, platform := range []string{"qq", "weixin", "telegram", "shanliao"} {
		for _, account := range []string{"9007199254740993", "9007199254740994"} {
			req := ImportRequest{Platform: platform, AccountID: account, Messages: []ImportMessage{
				importMessage("same-message-id", "peer-one", "项目会议", "2026-01-01T12:00:00+08:00"),
				importMessage("same-message-id", "peer-two", "另一个同名好友", "2026-01-01T04:01:00Z"),
			}}
			result := mustImport(t, s, req)
			if result.Inserted != 2 || result.Conversations != 2 || result.Duplicates != 0 {
				t.Fatalf("unexpected import result: %+v", result)
			}
			duplicate := mustImport(t, s, req)
			if duplicate.Inserted != 0 || duplicate.Duplicates != 2 {
				t.Fatal("replay did not deduplicate within the same scoped identity")
			}
		}
	}
	page := mustConversations(t, s, Filter{})
	if len(page.Items) != 16 {
		t.Fatalf("same-name identities merged: got %d conversations", len(page.Items))
	}
	ids := map[string]bool{}
	for _, conversation := range page.Items {
		if ids[conversation.ID] || conversation.UnreadCount != 1 || conversation.Title != "同名好友" {
			t.Fatal("distinct scoped identity or unread count differs")
		}
		ids[conversation.ID] = true
	}
	connections, err := s.Connections()
	if err != nil || len(connections) != 8 {
		t.Fatalf("connections differ: count=%d err=%v", len(connections), err)
	}
	for _, connection := range connections {
		if connection.MessageCount != 2 || connection.Source != "import" || connection.DisplayName != connection.AccountID {
			t.Fatal("connection source or aggregate message count differs")
		}
	}
	filtered := mustConversations(t, s, Filter{Platform: "qq", Q: "会议"})
	if len(filtered.Items) != 2 {
		t.Fatal("Chinese search escaped its platform predicate or did not match message evidence")
	}
	for _, term := range []string{"会", "项目会议"} {
		if len(mustConversations(t, s, Filter{Platform: "qq", Q: term}).Items) != 2 {
			t.Fatal("Chinese short/long literal search failed")
		}
	}
	var ftsMatches int
	if s.db.QueryRow(`SELECT count(*) FROM messages_fts WHERE messages_fts MATCH ?`, `"项目会议"`).Scan(&ftsMatches) != nil || ftsMatches != 8 {
		t.Fatal("Chinese FTS indexing not available")
	}
	for _, q := range []string{"' OR 1=1 --", "%", "*"} {
		if len(mustConversations(t, s, Filter{Q: q}).Items) != 0 {
			t.Fatal("SQL wildcard syntax was not treated as literal text")
		}
	}
}

func TestImportRollbackConflictAndValidationAreAtomic(t *testing.T) {
	s := testStore(t)
	base := ImportRequest{Platform: "qq", AccountID: "self", Messages: []ImportMessage{
		importMessage("1", "friend", "原始正文", "2026-01-01T04:00:00Z"),
	}}
	mustImport(t, s, base)
	conflict := ImportRequest{Platform: "qq", AccountID: "self", Messages: []ImportMessage{
		importMessage("new", "new-friend", "应回滚的新记录", "2026-01-01T04:01:00Z"),
		importMessage("1", "friend", "冲突正文", "2026-01-01T04:00:00Z"),
	}}
	result, err := s.Import(conflict)
	if !errors.Is(err, ErrConflict) || result != (ImportResult{}) {
		t.Fatal("conflict did not return sanitized zero-result error")
	}
	if len(mustConversations(t, s, Filter{}).Items) != 1 {
		t.Fatal("conflict batch left a partial contact or conversation")
	}
	invalid := conflict
	invalid.Messages = append([]ImportMessage(nil), conflict.Messages...)
	invalid.Messages[1].Body = strings.Repeat("x", 16001)
	if _, err := s.Import(invalid); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("oversized body was accepted")
	}
	if len(mustConversations(t, s, Filter{}).Items) != 1 {
		t.Fatal("invalid batch was partly written")
	}
	for name, mutate := range map[string]func(*ImportRequest){
		"platform":  func(r *ImportRequest) { r.Platform = "unknown" },
		"account":   func(r *ImportRequest) { r.AccountID = " a" },
		"control":   func(r *ImportRequest) { r.AccountID = "a\n" },
		"direction": func(r *ImportRequest) { r.Messages[0].Direction = "system" },
		"time":      func(r *ImportRequest) { r.Messages[0].OccurredAt = "2099-01-01T00:00:00Z" },
		"null":      func(r *ImportRequest) { r.Messages[0].Body = "a\x00b" },
	} {
		t.Run(name, func(t *testing.T) {
			req := base
			req.Messages = append([]ImportMessage(nil), base.Messages...)
			mutate(&req)
			if _, err := s.Import(req); !errors.Is(err, ErrInvalidInput) {
				t.Fatal("invalid import was not rejected")
			}
		})
	}
	// Equivalent offset timestamps normalize before replay/conflict comparison.
	equivalent := base
	equivalent.Messages = append([]ImportMessage(nil), base.Messages...)
	equivalent.Messages[0].OccurredAt = "2026-01-01T12:00:00+08:00"
	if result := mustImport(t, s, equivalent); result.Duplicates != 1 {
		t.Fatal("equivalent timestamp offset was treated as a conflict")
	}
}

func TestConversationStatePaginationAndCursorIsolation(t *testing.T) {
	s := testStore(t)
	req := ImportRequest{Platform: "qq", AccountID: "self", Messages: []ImportMessage{}}
	for i := 0; i < 5; i++ {
		req.Messages = append(req.Messages, importMessage("one", fmt.Sprintf("peer-%d", i), "项目", fmt.Sprintf("2026-01-01T04:%02d:00Z", i)))
	}
	mustImport(t, s, req)
	first := mustConversations(t, s, Filter{Limit: 2})
	if len(first.Items) != 2 || first.NextCursor == nil {
		t.Fatal("conversation page boundary missing")
	}
	seen := map[string]bool{}
	for _, item := range first.Items {
		seen[item.ID] = true
	}
	page := first
	for page.NextCursor != nil {
		page = mustConversations(t, s, Filter{Limit: 2, Cursor: *page.NextCursor})
		for _, item := range page.Items {
			if seen[item.ID] {
				t.Fatal("conversation pagination repeated an item")
			}
			seen[item.ID] = true
		}
	}
	if len(seen) != 5 {
		t.Fatal("conversation pagination omitted records")
	}
	if _, err := s.Conversations(Filter{Limit: 2, Cursor: *first.NextCursor, Platform: "weixin"}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatal("cursor reused across filters")
	}
	chosen := first.Items[0].ID
	if err := s.UpdateConversation(chosen, ConversationPatch{Read: boolValue(true), Pinned: boolValue(true), Archived: boolValue(true)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Conversations(Filter{Cursor: *first.NextCursor}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatal("mutation did not expire snapshot cursor")
	}
	archived := mustConversations(t, s, Filter{Archived: true, Pinned: true})
	if len(archived.Items) != 1 || archived.Items[0].ID != chosen || archived.Items[0].UnreadCount != 0 {
		t.Fatal("combined read/pin/archive patch was not applied")
	}
	if len(mustConversations(t, s, Filter{Unread: true}).Items) != 4 {
		t.Fatal("archived/read filters leaked the updated record")
	}
	if err := s.UpdateConversation(chosen, ConversationPatch{Read: boolValue(false), Archived: boolValue(false)}); err != nil {
		t.Fatal(err)
	}
	pinned := mustConversations(t, s, Filter{})
	if pinned.Items[0].ID != chosen || pinned.Items[0].UnreadCount != 1 || !pinned.Items[0].Pinned {
		t.Fatal("pinned priority or mark unread did not persist")
	}
}

func TestMessagePagesAreChronologicalAndScoped(t *testing.T) {
	s := testStore(t)
	req := ImportRequest{Platform: "telegram", AccountID: "self", Messages: []ImportMessage{}}
	for i := 0; i < 7; i++ {
		m := importMessage(fmt.Sprint(i+1), "friend", fmt.Sprintf("message-%d", i), fmt.Sprintf("2026-01-01T04:%02d:00Z", i/2))
		if i%2 == 0 {
			m.Direction = "outgoing"
		}
		req.Messages = append(req.Messages, m)
	}
	req.Messages = append(req.Messages, importMessage("1", "other-friend", "other", "2026-01-01T05:00:00Z"))
	mustImport(t, s, req)
	conversations := mustConversations(t, s, Filter{})
	var conversationID, otherID string
	for _, c := range conversations.Items {
		if c.PeerID == "friend" {
			conversationID = c.ID
		} else {
			otherID = c.ID
		}
	}
	seen := map[string]bool{}
	cursor := ""
	var firstCursor string
	for {
		page, err := s.Messages(conversationID, cursor, 3)
		if err != nil {
			t.Fatal(err)
		}
		for i, m := range page.Items {
			if seen[m.ID] || m.ConversationID != conversationID || i > 0 && page.Items[i-1].OccurredAt > m.OccurredAt {
				t.Fatal("message page repeated, escaped scope, or reversed chronology")
			}
			if m.Direction == "outgoing" && !m.Read {
				t.Fatal("outgoing import counted as unread")
			}
			seen[m.ID] = true
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
		if firstCursor == "" {
			firstCursor = cursor
		}
	}
	if len(seen) != 7 {
		t.Fatal("message pagination lost same-timestamp records")
	}
	if _, err := s.Messages(otherID, firstCursor, 3); !errors.Is(err, ErrInvalidCursor) {
		t.Fatal("message cursor crossed conversations")
	}
	if _, err := s.Messages(stableID("unknown"), "", 3); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing conversation did not return not found")
	}
	if _, err := s.Messages("not-a-resource-id", "", 3); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("malformed conversation identity accepted")
	}
}

func TestImportConcurrentReplayAndForeignKeys(t *testing.T) {
	s := testStore(t)
	req := ImportRequest{Platform: "qq", AccountID: "self", Messages: []ImportMessage{importMessage("one", "friend", "唯一正文", "2026-01-01T00:00:00Z")}}
	var wait sync.WaitGroup
	results := make(chan ImportResult, 10)
	errorsFound := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, err := s.Import(req)
			results <- result
			errorsFound <- err
		}()
	}
	wait.Wait()
	close(results)
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Fatalf("concurrent replay failed: %v", err)
		}
	}
	inserted, duplicate := 0, 0
	for result := range results {
		inserted += result.Inserted
		duplicate += result.Duplicates
	}
	if inserted != 1 || duplicate != 9 {
		t.Fatal("concurrent replay bypassed uniqueness")
	}
	if _, err := s.db.Exec(`INSERT INTO contacts(id,workspace_id,connection_id,peer_id,display_name) VALUES ('invalid','local-default','missing-connection','peer','name')`); err == nil {
		t.Fatal("foreign key accepted missing connection")
	}
	if _, err := s.db.Exec(`INSERT INTO workspaces(id) VALUES ('another-workspace')`); err == nil {
		t.Fatal("single-workspace guard was bypassed")
	}
	if _, err := s.Conversations(Filter{Cursor: "not-base64"}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatal("malformed cursor was accepted")
	}
	if err := s.UpdateConversation(stableID("missing"), ConversationPatch{Read: boolValue(true)}); !errors.Is(err, ErrNotFound) {
		t.Fatal("unknown mutation target was accepted")
	}
}
