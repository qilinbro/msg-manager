package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func telegramAgeSyntheticCursor(t *testing.T, s *Store, age time.Duration) TelegramBotCursor {
	t.Helper()
	// Only the encrypted t.TempDir fixture is changed. Advancing persisted
	// receipt age avoids clocks, sleeps, platform calls and production data.
	last := time.Now().UTC().Add(-age).Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`UPDATE telegram_bot_cursors SET last_event_at=? WHERE workspace_id=? AND bot_id=?`, last, workspaceID, telegramTestBot); err != nil {
		t.Fatal(err)
	}
	return telegramLoadCursor(t, s)
}

func TestTelegramBotCursorLeaseUsesOnlyNonemptyReceiptAge(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name, offset, last, want string
	}{
		{"fresh-exact-max-offset", "9223372036854775807", now.Add(-23 * time.Hour).Format(time.RFC3339Nano), "9223372036854775807"},
		{"just-before-expiry", "900000001", now.Add(-telegramBotCursorLease + time.Nanosecond).Format(time.RFC3339Nano), "900000001"},
		{"at-expiry", "900000001", now.Add(-telegramBotCursorLease).Format(time.RFC3339Nano), "0"},
		{"eight-day-idle", "900000001", now.Add(-8 * 24 * time.Hour).Format(time.RFC3339Nano), "0"},
		{"missing-time", "900000001", "", "0"},
		{"invalid-time", "900000001", "not-a-timestamp", "0"},
		{"clock-moved-backward", "900000001", now.Add(time.Second).Format(time.RFC3339Nano), "0"},
		{"initial-empty", "0", "", "0"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cursor := TelegramBotCursor{NextOffset: tt.offset, Revision: 11, Epoch: 3, LastEventAt: tt.last, UpdatedAt: now.Format(time.RFC3339Nano)}
			before := cursor
			if got := cursor.PollOffset(now); got != tt.want || cursor != before {
				t.Fatalf("request offset=%s want=%s or read modified cursor", got, tt.want)
			}
		})
	}
}

func TestTelegramBotIdleEpochAcceptsLowerReusedIDsAndRejectsABA(t *testing.T) {
	s := testStore(t)
	oldPage := telegramStorePage(telegramStoreEvent(200000000, "42", "7", "old first"), telegramStoreEvent(900000000, "42", "8", "old last"))
	telegramCommit(t, s, "0", oldPage)
	conversation := mustConversations(t, s, Filter{}).Items[0]
	if err := s.UpdateConversation(conversation.ID, ConversationPatch{Read: boolValue(true), Pinned: boolValue(true), Archived: boolValue(true)}); err != nil {
		t.Fatal(err)
	}
	stale := telegramAgeSyntheticCursor(t, s, 8*24*time.Hour)
	if stale.NextOffset != "900000001" || stale.PollOffset(time.Now()) != "0" {
		t.Fatal("idle acknowledgement was not withdrawn before a platform call")
	}
	// Both event IDs collide with an earlier epoch, and the final offset is
	// deliberately identical (ABA). Distinct stable private message IDs are
	// legitimate new messages; event IDs alone cannot identify them forever.
	newPage := telegramStorePage(telegramStoreEvent(200000000, "42", "9", "new low ID"), telegramStoreEvent(900000000, "42", "10", "new same final ID"))
	got, err := s.CommitTelegramBot(context.Background(), telegramTestBot, telegramTestPeers, stale, "0", newPage)
	if err != nil || got.Imported != 2 || got.Duplicates != 0 || got.AIQueued != 0 || got.NextOffset != stale.NextOffset {
		t.Fatalf("idle page rejected or misclassified: %+v %v", got, err)
	}
	current := telegramLoadCursor(t, s)
	if current.Epoch != stale.Epoch+1 || current.Revision != stale.Revision+1 || current.LastEventAt == stale.LastEventAt || current.NextOffset != stale.NextOffset {
		t.Fatal("epoch/revision did not fence repeated numeric watermark")
	}
	for i := 0; i < 1000; i++ {
		result, err := s.CommitTelegramBot(context.Background(), telegramTestBot, telegramTestPeers, stale, "0", newPage)
		if !errors.Is(err, ErrTelegramCursorChanged) || !reflect.DeepEqual(result, TelegramBotCommitResult{}) {
			t.Fatalf("stale prior epoch committed at replay %d", i)
		}
	}
	if telegramCount(t, s, "messages") != 4 || telegramCount(t, s, "telegram_bot_events") != 4 || telegramLoadCursor(t, s) != current {
		t.Fatal("old epoch replay changed graph, events or cursor")
	}
	// Stable message dedupe remains global across epochs, preserving user read
	// state even when a later update repeats an old private message.
	duplicate := telegramStorePage(telegramStoreEvent(900000001, "42", "7", "old first"))
	result := telegramCommit(t, s, current.NextOffset, duplicate)
	if result.Imported != 0 || result.Duplicates != 1 || telegramCount(t, s, "messages") != 4 || telegramCount(t, s, "telegram_bot_events") != 5 || telegramCount(t, s, "profile_jobs") != 0 {
		t.Fatal("epoch reset weakened stable message dedupe or queued AI")
	}
	archived := mustConversations(t, s, Filter{Archived: true}).Items
	if len(archived) != 1 || !archived[0].Pinned {
		t.Fatal("epoch reset changed user archive/pin state")
	}
	messages, err := s.SearchMessages(conversation.ID, "", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range messages.Items {
		if message.Body == "old first" && !message.Read {
			t.Fatal("duplicate across epochs reset read state")
		}
	}
}

func TestTelegramBotEmptyPollDoesNotRenewLeaseAndEmptyResetCommitsAtomically(t *testing.T) {
	s := testStore(t)
	telegramCommit(t, s, "0", telegramStorePage(telegramStoreEvent(900000000, "42", "7", "old")))
	before := telegramAgeSyntheticCursor(t, s, 23*time.Hour)
	empty := telegramStorePage()
	empty.NextOffset = before.NextOffset
	if _, err := s.CommitTelegramBot(context.Background(), telegramTestBot, telegramTestPeers, before, before.NextOffset, empty); err != nil {
		t.Fatal(err)
	}
	afterEmpty := telegramLoadCursor(t, s)
	if afterEmpty.LastEventAt != before.LastEventAt || afterEmpty.Epoch != before.Epoch || afterEmpty.Revision != before.Revision+1 {
		t.Fatal("empty poll extended the nonempty receipt lease")
	}
	expired := telegramAgeSyntheticCursor(t, s, 8*24*time.Hour)
	if _, err := s.CommitTelegramBot(context.Background(), telegramTestBot, telegramTestPeers, expired, expired.NextOffset, empty); !errors.Is(err, ErrTelegramCursorChanged) {
		t.Fatal("expired offset was allowed to remain an acknowledgement")
	}
	if telegramLoadCursor(t, s) != expired {
		t.Fatal("rejected stale fetch changed state")
	}
	resetEmpty := telegramStorePage()
	result, err := s.CommitTelegramBot(context.Background(), telegramTestBot, telegramTestPeers, expired, "0", resetEmpty)
	if err != nil || result.NextOffset != "0" || result.Events != 0 || result.Imported != 0 {
		t.Fatalf("empty epoch reset: %+v %v", result, err)
	}
	reset := telegramLoadCursor(t, s)
	if reset.NextOffset != "0" || reset.LastEventAt != "" || reset.Epoch != expired.Epoch+1 || reset.Revision != expired.Revision+1 || reset.PollOffset(time.Now()) != "0" {
		t.Fatal("empty reset left an old watermark or fabricated nonempty activity")
	}
	if telegramCount(t, s, "telegram_bot_events") != 1 || telegramCount(t, s, "messages") != 1 {
		t.Fatal("empty reset modified existing event/message data")
	}
	newPage := telegramStorePage(telegramStoreEvent(200000000, "42", "8", "after idle"))
	if _, err := s.CommitTelegramBot(context.Background(), telegramTestBot, telegramTestPeers, reset, "0", newPage); err != nil {
		t.Fatal("cannot resume after an empty idle queue", err)
	}
	telegramCursor(t, s, "200000001")
}

func TestTelegramBotResetCannotBeRequestedAgainstFreshOrForgedSnapshot(t *testing.T) {
	s := testStore(t)
	telegramCommit(t, s, "0", telegramStorePage(telegramStoreEvent(900000000, "42", "7", "original")))
	fresh := telegramLoadCursor(t, s)
	low := telegramStorePage(telegramStoreEvent(200000000, "42", "8", "must not commit"))
	if _, err := s.CommitTelegramBot(context.Background(), telegramTestBot, telegramTestPeers, fresh, "0", low); !errors.Is(err, ErrTelegramCursorChanged) {
		t.Fatal("caller forced a fresh epoch reset")
	}
	forged := fresh
	forged.LastEventAt = time.Now().Add(-8 * 24 * time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := s.CommitTelegramBot(context.Background(), telegramTestBot, telegramTestPeers, forged, "0", low); !errors.Is(err, ErrTelegramCursorChanged) {
		t.Fatal("caller-forged lease changed the epoch")
	}
	if telegramLoadCursor(t, s) != fresh || telegramCount(t, s, "messages") != 1 || telegramCount(t, s, "telegram_bot_events") != 1 {
		t.Fatal("rejected reset partially committed")
	}
}

func TestTelegramBotEpochResetLateFailureRollsBackAllState(t *testing.T) {
	for _, failure := range []string{"message-conflict", "late-sql"} {
		t.Run(failure, func(t *testing.T) {
			s := testStore(t)
			telegramCommit(t, s, "0", telegramStorePage(telegramStoreEvent(900000000, "42", "7", "original")))
			expired := telegramAgeSyntheticCursor(t, s, 8*24*time.Hour)
			revision := telegramRevision(t, s)
			second := telegramStoreEvent(200000001, "42", "7", "conflicting body")
			wantError := ErrConflict
			if failure == "late-sql" {
				if _, err := s.db.Exec(`CREATE TRIGGER synthetic_epoch_abort BEFORE INSERT ON telegram_bot_events WHEN NEW.epoch=1 AND NEW.update_id='200000001' BEGIN SELECT RAISE(ABORT,'synthetic epoch failure'); END`); err != nil {
					t.Fatal(err)
				}
				second = telegramStoreEvent(200000001, "42", "9", "new second")
				wantError = ErrUnavailable
			}
			page := telegramStorePage(telegramStoreEvent(200000000, "43", "8", "epochRollbackOnly"), second)
			result, err := s.CommitTelegramBot(context.Background(), telegramTestBot, telegramTestPeers, expired, "0", page)
			if !errors.Is(err, wantError) || !reflect.DeepEqual(result, TelegramBotCommitResult{}) {
				t.Fatalf("epoch failure returned partial acknowledgement: %+v %v", result, err)
			}
			if telegramLoadCursor(t, s) != expired || telegramRevision(t, s) != revision {
				t.Fatal("failed reset changed cursor/epoch/revision/timestamp")
			}
			for table, want := range map[string]int{"contacts": 1, "conversations": 1, "messages": 1, "telegram_bot_messages": 1, "telegram_bot_events": 1, "profile_jobs": 0} {
				if telegramCount(t, s, table) != want {
					t.Fatalf("epoch reset partially wrote %s", table)
				}
			}
			var matches int
			if err := s.db.QueryRow(`SELECT count(*) FROM messages_fts WHERE messages_fts MATCH 'epochRollbackOnly'`).Scan(&matches); err != nil || matches != 0 {
				t.Fatal("failed reset leaked text into FTS")
			}
		})
	}
}

func TestTelegramBotMigrationReplayPreservesMultipleEpochs(t *testing.T) {
	s := testStore(t)
	telegramCommit(t, s, "0", telegramStorePage(telegramStoreEvent(10, "42", "7", "old epoch")))
	expired := telegramAgeSyntheticCursor(t, s, 8*24*time.Hour)
	if _, err := s.CommitTelegramBot(context.Background(), telegramTestBot, telegramTestPeers, expired, "0", telegramStorePage(telegramStoreEvent(10, "42", "8", "new epoch"))); err != nil {
		t.Fatal(err)
	}
	before := telegramLoadCursor(t, s)
	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version=15`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(s.db); err != nil {
		t.Fatal("schema15 additive replay must preserve an existing epoch graph", err)
	}
	if telegramLoadCursor(t, s) != before || telegramCount(t, s, "telegram_bot_events") != 2 || telegramCount(t, s, "messages") != 2 || telegramCount(t, s, "feishu_configs") != 1 || telegramCount(t, s, "feishu_schedules") != 1 {
		t.Fatal("idempotent migration changed Telegram/Feishu state")
	}
}

func TestTelegramBotEpochAndRevisionNeverOverflowDuringReset(t *testing.T) {
	for field, statement := range map[string]string{
		"revision": `UPDATE telegram_bot_cursors SET revision=9223372036854775807`,
		"epoch":    `UPDATE telegram_bot_cursors SET epoch=9223372036854775807`,
	} {
		t.Run(field, func(t *testing.T) {
			s := testStore(t)
			telegramCommit(t, s, "0", telegramStorePage(telegramStoreEvent(900000000, "42", "7", "old")))
			if _, err := s.db.Exec(statement); err != nil {
				t.Fatal(err)
			}
			expired := telegramAgeSyntheticCursor(t, s, 8*24*time.Hour)
			result, err := s.CommitTelegramBot(context.Background(), telegramTestBot, telegramTestPeers, expired, "0", telegramStorePage(telegramStoreEvent(200000000, "42", "8", "new")))
			if !errors.Is(err, ErrUnavailable) || !reflect.DeepEqual(result, TelegramBotCommitResult{}) || telegramLoadCursor(t, s) != expired || telegramCount(t, s, "messages") != 1 || telegramCount(t, s, "telegram_bot_events") != 1 {
				t.Fatal("overflow changed a watermark, epoch or message graph")
			}
		})
	}
}

func TestTelegramBotCanceledEpochResetNeverChangesDurableState(t *testing.T) {
	s := testStore(t)
	telegramCommit(t, s, "0", telegramStorePage(telegramStoreEvent(900000000, "42", "7", "old")))
	expired := telegramAgeSyntheticCursor(t, s, 8*24*time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := s.CommitTelegramBot(ctx, telegramTestBot, telegramTestPeers, expired, "0", telegramStorePage(telegramStoreEvent(200000000, "42", "8", "new")))
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(result, TelegramBotCommitResult{}) || telegramLoadCursor(t, s) != expired || telegramCount(t, s, "messages") != 1 || telegramCount(t, s, "telegram_bot_events") != 1 {
		t.Fatal("canceled reset changed a watermark, epoch or message graph")
	}
}
