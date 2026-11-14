package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/qilinbro/msg-manager/migrations"
)

func TestWechatMonitorDefaultsPersistenceAndAccountIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "monitor.db")
	s, err := Open(path, testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	wantDefault := WechatMonitorSettings{PeerIDs: []string{}, IntervalSeconds: 30}
	settings, err := s.LoadWechatMonitor(ctx, "wxid_first")
	if err != nil || !reflect.DeepEqual(settings, wantDefault) {
		t.Fatal("new account was not disabled with explicit empty peers", settings, err)
	}
	want := WechatMonitorSettings{Enabled: true, PeerIDs: []string{"wxid_friend_a", "wxid_friend_b"}, IntervalSeconds: 10}
	if err := s.SaveWechatMonitor(ctx, "wxid_first", want); err != nil {
		t.Fatal(err)
	}
	if other, err := s.LoadWechatMonitor(ctx, "wxid_second"); err != nil || !reflect.DeepEqual(other, wantDefault) {
		t.Fatal("monitor selection crossed account scope", other, err)
	}
	var connectionCount int
	if err := s.db.QueryRow(`SELECT count(*) FROM connections`).Scan(&connectionCount); err != nil || connectionCount != 0 {
		t.Fatal("saving selection fabricated a platform connection", connectionCount, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path, testKey)
	if err != nil {
		t.Fatal("reopen encrypted monitor store", err)
	}
	defer s.Close()
	if got, err := s.LoadWechatMonitor(ctx, "wxid_first"); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("monitor selection did not survive reopen", got, err)
	}
	if err := s.SaveWechatMonitor(ctx, "wxid_first", WechatMonitorSettings{IntervalSeconds: 300}); err != nil {
		t.Fatal("disable and clear selection", err)
	}
	got, err := s.LoadWechatMonitor(ctx, "wxid_first")
	if err != nil || got.Enabled || got.PeerIDs == nil || len(got.PeerIDs) != 0 || got.IntervalSeconds != 300 {
		t.Fatal("disabled selection was not cleared", got, err)
	}
}

func TestWechatMonitorInvalidUpdatesPreserveExistingSelection(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	baseline := WechatMonitorSettings{Enabled: true, PeerIDs: []string{"wxid_peer"}, IntervalSeconds: 30}
	if err := s.SaveWechatMonitor(ctx, "wxid_self", baseline); err != nil {
		t.Fatal(err)
	}
	invalid := []WechatMonitorSettings{
		{Enabled: true, IntervalSeconds: 30},
		{Enabled: false, IntervalSeconds: 0},
		{IntervalSeconds: 9},
		{IntervalSeconds: 301},
		{PeerIDs: []string{"wxid_peer", "wxid_peer"}, IntervalSeconds: 30},
		{PeerIDs: []string{"wxid_self"}, IntervalSeconds: 30},
	}
	for _, id := range []string{"", "a", " wxid_peer", "wxid_peer ", "wxid_peer\n", "wxid_peer\x00", "../private", `peer\path`, "room@chatroom", "gh_synthetic", "filehelper", "notification_messages", "helper_entry", "微信群", strings.Repeat("a", 129)} {
		invalid = append(invalid, WechatMonitorSettings{PeerIDs: []string{id}, IntervalSeconds: 30})
	}
	tooMany := WechatMonitorSettings{IntervalSeconds: 30}
	for i := 0; i < 21; i++ {
		tooMany.PeerIDs = append(tooMany.PeerIDs, fmt.Sprintf("wxid_peer_%d", i))
	}
	invalid = append(invalid, tooMany)
	for i, settings := range invalid {
		if err := ValidateWechatMonitorSettings("wxid_self", settings); !errors.Is(err, ErrInvalidInput) {
			t.Fatal("invalid selection validation accepted", i, err)
		}
		if err := s.SaveWechatMonitor(ctx, "wxid_self", settings); !errors.Is(err, ErrInvalidInput) {
			t.Fatal("invalid selection persisted", i, err)
		}
		if got, err := s.LoadWechatMonitor(ctx, "wxid_self"); err != nil || !reflect.DeepEqual(got, baseline) {
			t.Fatal("invalid update changed existing selection", i, got, err)
		}
	}
	for _, account := range []string{"", "../other", "filehelper", "gh_synthetic", "wxid_self\n"} {
		if _, err := s.LoadWechatMonitor(ctx, account); !errors.Is(err, ErrInvalidInput) {
			t.Fatal("invalid account load accepted", err)
		}
		if err := s.SaveWechatMonitor(ctx, account, baseline); !errors.Is(err, ErrInvalidInput) {
			t.Fatal("invalid account save accepted", err)
		}
	}
	boundary := WechatMonitorSettings{Enabled: true, IntervalSeconds: 300}
	for i := 0; i < 20; i++ {
		boundary.PeerIDs = append(boundary.PeerIDs, fmt.Sprintf("wxid_peer_%d", i))
	}
	boundary.PeerIDs[0] = strings.Repeat("a", 128)
	if err := s.SaveWechatMonitor(ctx, "wxid_self", boundary); err != nil {
		t.Fatal("valid peer count/length/interval boundary rejected", err)
	}
}

func TestWechatMonitorCancellationWhileWaitingDoesNotReadOrSave(t *testing.T) {
	s := testStore(t)
	valid := WechatMonitorSettings{Enabled: true, PeerIDs: []string{"wxid_peer"}, IntervalSeconds: 30}
	if _, err := s.LoadWechatMonitor(nil, "wxid_self"); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("nil load context accepted", err)
	}
	if err := s.SaveWechatMonitor(nil, "wxid_self", valid); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("nil save context accepted", err)
	}
	for _, save := range []bool{false, true} {
		t.Run(fmt.Sprintf("save=%v", save), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			observed := &observedImportContext{Context: ctx, checked: make(chan struct{})}
			s.mu.Lock()
			defer s.mu.Unlock()
			done := make(chan error, 1)
			go func() {
				if save {
					done <- s.SaveWechatMonitor(observed, "wxid_self", valid)
				} else {
					_, err := s.LoadWechatMonitor(observed, "wxid_self")
					done <- err
				}
			}()
			<-observed.checked
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatal("waiting operation did not return cancellation", err)
				}
			case <-time.After(time.Second):
				t.Fatal("canceled operation kept waiting for mutex release")
			}
		})
	}
	got, err := s.LoadWechatMonitor(context.Background(), "wxid_self")
	if err != nil || got.Enabled || len(got.PeerIDs) != 0 {
		t.Fatal("canceled save mutated selection", got, err)
	}
}

func TestWeChatImportCancellationReturnsBeforeStoreMutexIsReleased(t *testing.T) {
	s := testStore(t)
	before := snapshotV3Data(t, s.db)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observed := &observedImportContext{Context: ctx, checked: make(chan struct{})}
	s.mu.Lock()
	locked := true
	defer func() {
		if locked {
			s.mu.Unlock()
		}
	}()
	done := make(chan error, 1)
	go func() {
		_, err := s.ImportWeChatContext(observed, wechatBatch("wxid_canceled"))
		done <- err
	}()
	<-observed.checked
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("canceled import did not return its cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled import kept waiting behind the database mutex")
	}
	// Cancellation completed while this goroutine still owns the mutex. No
	// importer is left queued to wake up and write when the mutex is released.
	s.mu.Unlock()
	locked = false
	if !reflect.DeepEqual(before, snapshotV3Data(t, s.db)) {
		t.Fatal("canceled import changed the existing database")
	}
}

func TestWechatMonitorSQLCancellationAndCorruptSettingsFailClosed(t *testing.T) {
	s := testStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	connection, err := s.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	waits := s.db.Stats().WaitCount
	done := make(chan error, 1)
	go func() {
		done <- s.SaveWechatMonitor(ctx, "wxid_self", WechatMonitorSettings{Enabled: true, PeerIDs: []string{"wxid_peer"}, IntervalSeconds: 30})
	}()
	deadline := time.Now().Add(time.Second)
	for s.db.Stats().WaitCount == waits {
		if time.Now().After(deadline) {
			t.Fatal("save did not reach the blocked SQL connection")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("blocked SQL did not honor cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("SQL cancellation did not return")
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := s.LoadWechatMonitor(context.Background(), "wxid_self"); err != nil || got.Enabled {
		t.Fatal("canceled SQL save persisted", got, err)
	}
	if _, err := s.db.Exec(`INSERT INTO wechat_monitor_settings VALUES('local-default','wxid_self',1,'null',30,'2026-11-14T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadWechatMonitor(context.Background(), "wxid_self"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("corrupted persisted selection was treated as enabled/default", err)
	}
}

func TestWechatMonitorV4UpgradePreservesMessageAndAIGraph(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-v4.db")
	legacy := legacyV3Store(t, path)
	raw, err := migrations.Files.ReadFile("0004_wechat_source.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.db.Exec(string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.db.Exec(`INSERT INTO schema_migrations VALUES(4,'2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.ImportWeChat(wechatBatch("wxid_self")); err != nil {
		t.Fatal(err)
	}
	conversation := mustConversations(t, legacy, Filter{}).Items[0].ID
	if err := legacy.UpdateConversation(conversation, ConversationPatch{Read: boolValue(true), Pinned: boolValue(true)}); err != nil {
		t.Fatal(err)
	}
	contextBefore, err := legacy.AIContext(conversation)
	if err != nil {
		t.Fatal(err)
	}
	result := aiTestResult(contextBefore)
	result.MemoryCandidates = []AICandidate{{Content: "合成v4记忆保留", EvidenceIDs: []string{contextBefore.Messages[0].ID}}}
	if _, err := legacy.SaveAIAnalysis(conversation, contextBefore.ContextHash, result); err != nil {
		t.Fatal(err)
	}
	before := snapshotV3Data(t, legacy.db)
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path, testKey)
	if err != nil {
		t.Fatal("v4 to v5 migration", err)
	}
	defer s.Close()
	if !reflect.DeepEqual(before, snapshotV3Data(t, s.db)) {
		t.Fatal("monitor migration changed the existing message/AI graph")
	}
	var version, oldMigrations, sources int
	if err := s.db.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&version); err != nil || version != 5 {
		t.Fatal("v5 migration was not recorded", version, err)
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM schema_migrations WHERE version<=4 AND applied_at='2026-01-01T00:00:00Z'`).Scan(&oldMigrations); err != nil || oldMigrations != 4 {
		t.Fatal("existing migration history changed", oldMigrations, err)
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM messages WHERE connector_source='wechatdb'`).Scan(&sources); err != nil || sources != 1 {
		t.Fatal("migration lost source provenance", sources, err)
	}
	if got, err := s.LoadWechatMonitor(context.Background(), "wxid_self"); err != nil || got.Enabled || len(got.PeerIDs) != 0 {
		t.Fatal("migration enabled background reads without consent", got, err)
	}
	rows, err := s.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() || rows.Err() != nil {
		t.Fatal("v5 migration broke foreign keys")
	}
}
