package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qilinbro/msg-manager/internal/connector/telegrambot"
)

const telegramTestBot = "123456"

var telegramTestPeers = []string{"42", "43"}

func telegramStoreEvent(update int64, peer, messageID, body string) telegrambot.Event {
	return telegrambot.Event{UpdateID: strconv.FormatInt(update, 10), Type: "message", Message: &telegrambot.Message{
		ChatID: peer, MessageID: messageID, SenderID: peer, ChatType: "private", Body: body, Date: 1767225600}}
}
func telegramStorePage(events ...telegrambot.Event) telegrambot.Page {
	next := "0"
	if len(events) > 0 {
		id, _ := strconv.ParseInt(events[len(events)-1].UpdateID, 10, 64)
		next = strconv.FormatInt(id+1, 10)
	}
	return telegrambot.Page{Verification: telegrambot.Verification{BotID: telegramTestBot, IsBot: true}, Events: append([]telegrambot.Event{}, events...), NextOffset: next}
}
func telegramCount(t *testing.T, s *Store, table string) int {
	t.Helper()
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}
func telegramRevision(t *testing.T, s *Store) int {
	t.Helper()
	var revision int
	if err := s.db.QueryRow(`SELECT revision FROM workspaces WHERE id=?`, workspaceID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	return revision
}
func telegramCursor(t *testing.T, s *Store, want string) {
	t.Helper()
	cursor, err := s.LoadTelegramBotCursor(context.Background(), telegramTestBot)
	if err != nil || cursor.NextOffset != want {
		t.Fatalf("cursor=%+v err=%v want=%s", cursor, err, want)
	}
}
func telegramLoadCursor(t *testing.T, s *Store) TelegramBotCursor {
	t.Helper()
	cursor, err := s.LoadTelegramBotCursor(context.Background(), telegramTestBot)
	if err != nil {
		t.Fatal(err)
	}
	return cursor
}
func telegramCommit(t *testing.T, s *Store, offset string, page telegrambot.Page) TelegramBotCommitResult {
	t.Helper()
	cursor := telegramLoadCursor(t, s)
	if cursor.NextOffset != offset {
		t.Fatalf("fixture cursor=%s want=%s", cursor.NextOffset, offset)
	}
	result, err := s.CommitTelegramBot(context.Background(), telegramTestBot, telegramTestPeers, cursor, cursor.PollOffset(time.Now()), page)
	if err != nil {
		t.Fatalf("Telegram atomic commit: %v", err)
	}
	return result
}

func TestTelegramBotHundredMessagesThousandStaleReplaysAndHonestSources(t *testing.T) {
	s := testStore(t)
	telegramCursor(t, s, "0")
	if telegramCount(t, s, "telegram_bot_cursors") != 0 {
		t.Fatal("read-only cursor lookup created a row")
	}
	events := make([]telegrambot.Event, 100)
	for i := range events {
		events[i] = telegramStoreEvent(int64(i+10), telegramTestPeers[i%2], strconv.Itoa(i+1), fmt.Sprintf("  telegramSyntheticOnly 原文 @bot %d\n\t ", i))
	}
	page := telegramStorePage(events...)
	result := telegramCommit(t, s, "0", page)
	if result.Events != 100 || result.Imported != 100 || result.Duplicates != 0 || result.Conversations != 2 || result.NextOffset != "110" || result.AIQueued != 0 || result.SourceKind != "telegram_bot" || result.Partial {
		t.Fatalf("bad result: %+v", result)
	}
	initialRevision := telegramRevision(t, s)
	for i := 0; i < 1000; i++ {
		got, err := s.CommitTelegramBot(context.Background(), telegramTestBot, telegramTestPeers, TelegramBotCursor{NextOffset: "0"}, "0", page)
		if !errors.Is(err, ErrTelegramCursorChanged) || !reflect.DeepEqual(got, TelegramBotCommitResult{}) {
			t.Fatalf("stale replay %d acknowledged or changed page: %+v %v", i, got, err)
		}
	}
	for table, want := range map[string]int{"connections": 1, "contacts": 2, "conversations": 2, "messages": 100, "telegram_bot_messages": 100, "telegram_bot_events": 100, "telegram_bot_cursors": 1, "profile_jobs": 0, "contact_profiles": 0, "ai_call_reservations": 0} {
		if got := telegramCount(t, s, table); got != want {
			t.Fatalf("%s=%d want %d", table, got, want)
		}
	}
	telegramCursor(t, s, "110")
	if telegramRevision(t, s) != initialRevision {
		t.Fatal("stale replays changed workspace revision")
	}
	var fts int
	if err := s.db.QueryRow(`SELECT count(*) FROM messages_fts WHERE messages_fts MATCH 'telegramSyntheticOnly'`).Scan(&fts); err != nil || fts != 100 {
		t.Fatalf("FTS=%d err=%v", fts, err)
	}
	connections, err := s.Connections()
	if err != nil || len(connections) != 1 || connections[0].Platform != "telegram" || connections[0].Source != "telegram_bot" || connections[0].AccountID != "bot:"+telegramTestBot {
		t.Fatalf("connection provenance: %+v %v", connections, err)
	}
	conversations := mustConversations(t, s, Filter{Platform: "telegram", Limit: 100})
	if len(conversations.Items) != 2 {
		t.Fatal("private peers merged")
	}
	for _, conversation := range conversations.Items {
		messages, err := s.SearchMessages(conversation.ID, "", "", 100)
		if err != nil || len(messages.Items) != 50 {
			t.Fatalf("messages count or query error: %d %v", len(messages.Items), err)
		}
		for _, message := range messages.Items {
			if message.Source != "telegram_bot" || !strings.HasPrefix(message.ExternalID, "telegrambot:private:") || !strings.HasPrefix(message.Body, "  telegramSyntheticOnly") || !strings.HasSuffix(message.Body, "\n\t ") || message.Direction != "incoming" || message.Read {
				t.Fatal("message lost original content or authoritative source")
			}
		}
	}
}

func TestTelegramBotDuplicateMessagePreservesReadPinArchiveAndNoAI(t *testing.T) {
	s := testStore(t)
	body := "  保留 @mention 原文\n "
	page := telegramStorePage(telegramStoreEvent(10, "42", "7", body))
	telegramCommit(t, s, "0", page)
	conversation := mustConversations(t, s, Filter{}).Items[0]
	if err := s.UpdateConversation(conversation.ID, ConversationPatch{Read: boolValue(true), Pinned: boolValue(true), Archived: boolValue(true)}); err != nil {
		t.Fatal(err)
	}
	revision := telegramRevision(t, s)
	duplicate := telegramStorePage(telegramStoreEvent(11, "42", "7", body))
	result := telegramCommit(t, s, "11", duplicate)
	if result.Imported != 0 || result.Duplicates != 1 || result.Events != 1 || result.Conversations != 1 || result.AIQueued != 0 {
		t.Fatalf("duplicate=%+v", result)
	}
	if telegramRevision(t, s) != revision {
		t.Fatal("message duplicate reset aggregate revision")
	}
	archived := mustConversations(t, s, Filter{Archived: true}).Items
	if len(archived) != 1 || !archived[0].Pinned || !archived[0].Archived || archived[0].UnreadCount != 0 {
		t.Fatal("duplicate reset user state")
	}
	messages, err := s.SearchMessages(conversation.ID, "", "", 20)
	if err != nil || len(messages.Items) != 1 || !messages.Items[0].Read || messages.Items[0].Body != body {
		t.Fatal("duplicate changed message")
	}
	if telegramCount(t, s, "telegram_bot_events") != 2 || telegramCount(t, s, "telegram_bot_messages") != 1 || telegramCount(t, s, "profile_jobs") != 0 {
		t.Fatal("duplicate provenance or AI side effect")
	}
	telegramCursor(t, s, "12")
}

func TestTelegramBotExistingEventFingerprintDeduplicatesAndDetectsMismatch(t *testing.T) {
	s := testStore(t)
	page := telegramStorePage(telegramStoreEvent(10, "42", "7", "original"))
	telegramCommit(t, s, "0", page)
	// Synthetic migration/recovery fixture: an event already exists ahead of
	// the saved cursor. Normal stale API pages are rejected by CAS, not replayed.
	if _, err := s.db.Exec(`UPDATE telegram_bot_cursors SET next_offset='0'`); err != nil {
		t.Fatal(err)
	}
	result := telegramCommit(t, s, "0", page)
	if result.Imported != 0 || result.Duplicates != 1 || telegramCount(t, s, "messages") != 1 || telegramCount(t, s, "telegram_bot_events") != 1 {
		t.Fatal("event was not idempotent")
	}
	if _, err := s.db.Exec(`UPDATE telegram_bot_cursors SET next_offset='0'`); err != nil {
		t.Fatal(err)
	}
	changed := telegramStorePage(telegramStoreEvent(10, "42", "7", "changed"))
	got, err := s.CommitTelegramBot(context.Background(), telegramTestBot, telegramTestPeers, telegramLoadCursor(t, s), "0", changed)
	if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(got, TelegramBotCommitResult{}) {
		t.Fatal("event fingerprint mismatch acknowledged")
	}
	telegramCursor(t, s, "0")
}

func TestTelegramBotUnsupportedIsSanitizedAndAdvancesOnlyAtomicPage(t *testing.T) {
	s := testStore(t)
	page := telegramStorePage(telegramStoreEvent(10, "42", "7", "only accepted text"),
		telegrambot.Event{UpdateID: "11", Type: "message", Reason: "not_authorized"},
		telegrambot.Event{UpdateID: "12", Type: "message", Reason: "not_private"},
		telegrambot.Event{UpdateID: "13", Type: "message", Reason: "unsupported_media"},
		telegrambot.Event{UpdateID: "14", Type: "edited_message", Reason: "unsupported_edit"},
		telegrambot.Event{UpdateID: "15", Type: "unknown", Reason: "unsupported_event"},
		telegrambot.Event{UpdateID: "16", Type: "message", Reason: "malformed_message"})
	result := telegramCommit(t, s, "0", page)
	if result.Events != 7 || result.Imported != 1 || result.Unsupported != 4 || result.NotAuthorized != 2 || !result.Partial || result.NextOffset != "17" || result.AIQueued != 0 {
		t.Fatalf("partial result=%+v", result)
	}
	if telegramCount(t, s, "messages") != 1 || telegramCount(t, s, "contacts") != 1 || telegramCount(t, s, "telegram_bot_events") != 7 || telegramCount(t, s, "profile_jobs") != 0 {
		t.Fatal("unsupported generated content rows")
	}
	var withMessage int
	if err := s.db.QueryRow(`SELECT count(*) FROM telegram_bot_events WHERE reason<>'' AND message_id IS NOT NULL`).Scan(&withMessage); err != nil || withMessage != 0 {
		t.Fatal("unsupported event linked a body")
	}
	rows, err := s.db.Query(`PRAGMA table_info(telegram_bot_events)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primary int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primary); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(name, "body") || strings.Contains(name, "raw") || strings.Contains(name, "payload") || strings.Contains(name, "chat") {
			t.Fatal("unsupported table stores excessive payload")
		}
	}
	telegramCursor(t, s, "17")
}

func TestTelegramBotLateMessageConflictRollsBackGraphFTSEventsAndCursor(t *testing.T) {
	s := testStore(t)
	telegramCommit(t, s, "0", telegramStorePage(telegramStoreEvent(10, "42", "7", "original")))
	revision := telegramRevision(t, s)
	page := telegramStorePage(telegramStoreEvent(11, "43", "8", "rollbackSyntheticOnly"), telegramStoreEvent(12, "42", "7", "conflicting body"))
	got, err := s.CommitTelegramBot(context.Background(), telegramTestBot, telegramTestPeers, telegramLoadCursor(t, s), "11", page)
	if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(got, TelegramBotCommitResult{}) {
		t.Fatalf("conflict=%+v %v", got, err)
	}
	for table, want := range map[string]int{"contacts": 1, "conversations": 1, "messages": 1, "telegram_bot_messages": 1, "telegram_bot_events": 1} {
		if telegramCount(t, s, table) != want {
			t.Fatalf("partial write in %s", table)
		}
	}
	var matches int
	if err := s.db.QueryRow(`SELECT count(*) FROM messages_fts WHERE messages_fts MATCH 'rollbackSyntheticOnly'`).Scan(&matches); err != nil || matches != 0 {
		t.Fatal("rolled-back text reached FTS")
	}
	if telegramRevision(t, s) != revision {
		t.Fatal("failed page changed revision")
	}
	telegramCursor(t, s, "11")
}

func TestTelegramBotLateSQLFailureRollsBackNewCursorAndWholePage(t *testing.T) {
	s := testStore(t)
	revision := telegramRevision(t, s)
	if _, err := s.db.Exec(`CREATE TRIGGER synthetic_telegram_abort BEFORE INSERT ON telegram_bot_events WHEN NEW.update_id='11' BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`); err != nil {
		t.Fatal(err)
	}
	page := telegramStorePage(telegramStoreEvent(10, "42", "7", "first"), telegramStoreEvent(11, "43", "8", "second"))
	got, err := s.CommitTelegramBot(context.Background(), telegramTestBot, telegramTestPeers, TelegramBotCursor{NextOffset: "0"}, "0", page)
	if !errors.Is(err, ErrUnavailable) || !reflect.DeepEqual(got, TelegramBotCommitResult{}) {
		t.Fatalf("SQL failure leaked or succeeded: %+v %v", got, err)
	}
	for _, table := range []string{"connections", "contacts", "conversations", "messages", "telegram_bot_messages", "telegram_bot_events", "telegram_bot_cursors", "profile_jobs"} {
		if telegramCount(t, s, table) != 0 {
			t.Fatalf("partial SQL write in %s", table)
		}
	}
	if telegramRevision(t, s) != revision {
		t.Fatal("failed SQL changed workspace revision")
	}
	telegramCursor(t, s, "0")
}

func TestTelegramBotStoreMutexWaitAndPreCanceledWorkAreCancellable(t *testing.T) {
	s := testStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	page := telegramStorePage(telegramStoreEvent(10, "42", "7", "original"))
	s.mu.Lock()
	defer s.mu.Unlock()
	errorsOut := make(chan error, 2)
	go func() { _, err := s.LoadTelegramBotCursor(ctx, telegramTestBot); errorsOut <- err }()
	go func() {
		_, err := s.CommitTelegramBot(ctx, telegramTestBot, telegramTestPeers, TelegramBotCursor{NextOffset: "0"}, "0", page)
		errorsOut <- err
	}()
	cancel()
	for i := 0; i < 2; i++ {
		select {
		case err := <-errorsOut:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled store operation=%v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("store operation cannot cancel while mutex is held")
		}
	}
	if telegramCount(t, s, "messages") != 0 || telegramCount(t, s, "telegram_bot_cursors") != 0 {
		t.Fatal("canceled work wrote a page")
	}
}

func TestTelegramBotConcurrentSameCursorHasOneWinner(t *testing.T) {
	s := testStore(t)
	page := telegramStorePage(telegramStoreEvent(10, "42", "7", "original"))
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for i := 0; i < 2; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, err := s.CommitTelegramBot(context.Background(), telegramTestBot, telegramTestPeers, TelegramBotCursor{NextOffset: "0"}, "0", page)
			results <- err
		}()
	}
	close(start)
	group.Wait()
	close(results)
	successes, stale := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrTelegramCursorChanged) {
			stale++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || stale != 1 || telegramCount(t, s, "messages") != 1 || telegramCount(t, s, "telegram_bot_events") != 1 {
		t.Fatal("cursor compare-and-set admitted two pages")
	}
	telegramCursor(t, s, "11")
}

func TestTelegramBotStoreRepeatsAllScopeAndWatermarkValidation(t *testing.T) {
	s := testStore(t)
	for name, mutate := range map[string]func(*telegrambot.Page){
		"bot":                 func(p *telegrambot.Page) { p.BotID = "99" },
		"webhook":             func(p *telegrambot.Page) { p.WebhookPresent = true },
		"allowlist":           func(p *telegrambot.Page) { p.Events[0].Message.ChatID = "99" },
		"sender":              func(p *telegrambot.Page) { p.Events[0].Message.SenderID = "99" },
		"group":               func(p *telegrambot.Page) { p.Events[0].Message.ChatType = "group" },
		"watermark":           func(p *telegrambot.Page) { p.NextOffset = "999" },
		"body-on-unsupported": func(p *telegrambot.Page) { p.Events[0].Reason = "unsupported_media" },
		"unknown-reason":      func(p *telegrambot.Page) { p.Events[0].Message = nil; p.Events[0].Reason = "raw-sensitive-marker" },
		"oversized":           func(p *telegrambot.Page) { p.Events[0].Message.Body = strings.Repeat("x", 16001) },
	} {
		t.Run(name, func(t *testing.T) {
			page := telegramStorePage(telegramStoreEvent(10, "42", "7", "original"))
			mutate(&page)
			got, err := s.CommitTelegramBot(context.Background(), telegramTestBot, telegramTestPeers, TelegramBotCursor{NextOffset: "0"}, "0", page)
			if !errors.Is(err, ErrInvalidInput) || !reflect.DeepEqual(got, TelegramBotCommitResult{}) {
				t.Fatal("store trusted unsafe caller page")
			}
		})
	}
	if telegramCount(t, s, "messages") != 0 || telegramCount(t, s, "telegram_bot_cursors") != 0 {
		t.Fatal("invalid pages wrote data")
	}
}

func TestTelegramBotMigration14To15PreservesExistingDataAndFeishu(t *testing.T) {
	s := testStore(t)
	mustImport(t, s, ImportRequest{Platform: "telegram", AccountID: "synthetic-desktop-export", Messages: []ImportMessage{importMessage("legacy", "42", "legacy synthetic text", "2026-01-01T00:00:00Z")}})
	// This encrypted temporary fixture contains no real data or production DB.
	if _, err := s.db.Exec(`DROP TABLE telegram_bot_events; DROP TABLE telegram_bot_messages; DROP TABLE telegram_bot_cursors; DELETE FROM schema_migrations WHERE version=15`); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := s.db.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&version); err != nil || version != 14 {
		t.Fatal("fixture is not schema14")
	}
	if err := migrate(s.db); err != nil {
		t.Fatal(err)
	}
	if err := migrate(s.db); err != nil {
		t.Fatal("schema15 migration not idempotent")
	}
	if err := s.db.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&version); err != nil || version != 15 {
		t.Fatal("schema15 absent")
	}
	for table, want := range map[string]int{"messages": 1, "feishu_configs": 1, "feishu_schedules": 1, "telegram_bot_events": 0, "telegram_bot_messages": 0, "telegram_bot_cursors": 0} {
		if telegramCount(t, s, table) != want {
			t.Fatalf("migration modified %s", table)
		}
	}
	var body string
	if err := s.db.QueryRow(`SELECT body FROM messages`).Scan(&body); err != nil || body != "legacy synthetic text" {
		t.Fatal("migration changed existing content")
	}
	connections, err := s.Connections()
	if err != nil || len(connections) != 1 || connections[0].Source != "import" {
		t.Fatal("migration relabeled Desktop export")
	}
	telegramCommit(t, s, "0", telegramStorePage(telegramStoreEvent(10, "42", "7", "new Bot API text")))
	if telegramCount(t, s, "messages") != 2 {
		t.Fatal("migrated schema cannot commit Bot page")
	}
}
