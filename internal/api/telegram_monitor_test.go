package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qilinbro/msg-manager/internal/connector"
	"github.com/qilinbro/msg-manager/internal/connector/telegramuser"
	"github.com/qilinbro/msg-manager/internal/store"
)

type incrementalTelegramStub struct {
	*stubTelegram
	mu        sync.Mutex
	actual    string
	baselines map[string]int
	afters    map[string][]string
	batches   map[string]telegramuser.IncrementalBatch
	failures  map[string]error
	entered   chan struct{}
	block     bool
}

func (s *incrementalTelegramStub) Baseline(ctx context.Context, expected, peer string) (telegramuser.IncrementalBatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if expected != s.actual {
		return telegramuser.IncrementalBatch{}, &telegramuser.Error{Code: "identity_mismatch"}
	}
	s.baselines[peer]++
	return telegramuser.IncrementalBatch{Batch: telegramuser.Batch{Identity: telegramuser.Identity{AccountID: expected, Name: "Synthetic owner", ReadOnly: true}, PeerID: peer, PeerName: "Synthetic friend", Messages: []telegramuser.Message{}}, Cursor: "10"}, nil
}

func (s *incrementalTelegramStub) Scan(ctx context.Context, expected, peer, after string) (telegramuser.IncrementalBatch, error) {
	s.mu.Lock()
	s.afters[peer] = append(s.afters[peer], after)
	actual, block, entered := s.actual, s.block, s.entered
	batch, present := s.batches[peer]
	err := s.failures[peer]
	s.mu.Unlock()
	if actual != expected {
		return telegramuser.IncrementalBatch{}, &telegramuser.Error{Code: "identity_mismatch"}
	}
	if block {
		if entered != nil {
			select {
			case entered <- struct{}{}:
			default:
			}
		}
		<-ctx.Done()
		return telegramuser.IncrementalBatch{}, ctx.Err()
	}
	if err != nil {
		return telegramuser.IncrementalBatch{}, err
	}
	if present {
		return batch, nil
	}
	return telegramuser.IncrementalBatch{Batch: telegramuser.Batch{Identity: telegramuser.Identity{AccountID: expected, Name: "Synthetic owner", ReadOnly: true}, PeerID: peer, PeerName: "Synthetic friend", Messages: []telegramuser.Message{}}, Cursor: after}, nil
}

func bindIncrementalTelegram(t *testing.T, h *handler) (*incrementalTelegramStub, chan struct{}, chan time.Duration) {
	t.Helper()
	base := bindTelegram(h)
	base.dialogs.Items = append(base.dialogs.Items, telegramuser.Dialog{PeerID: "457", Name: "Other friend"})
	s := &incrementalTelegramStub{stubTelegram: base, actual: "123", baselines: map[string]int{}, afters: map[string][]string{}, batches: map[string]telegramuser.IncrementalBatch{}, failures: map[string]error{}}
	h.telegram.client = s
	ticks, cycles := make(chan struct{}, 8), make(chan time.Duration, 8)
	h.telegram.monitorWait = func(ctx context.Context, d time.Duration) bool {
		select {
		case cycles <- d:
		case <-ctx.Done():
			return false
		}
		select {
		case <-ticks:
			return true
		case <-ctx.Done():
			return false
		}
	}
	return s, ticks, cycles
}

func telegramMonitorCycle(t *testing.T, cycles <-chan time.Duration) time.Duration {
	t.Helper()
	select {
	case delay := <-cycles:
		return delay
	case <-time.After(5 * time.Second):
		t.Fatal("Telegram monitor did not complete a synthetic cycle")
		return 0
	}
}

func enableTelegramMonitor(t *testing.T, h *handler, peers ...string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"enabled": true, "expected_account_id": "123", "peer_ids": peers})
	w := emailTestRequest(h, "POST", telegramMonitorPath, string(body), true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func telegramSavedMonitor(t *testing.T, h *handler) telegramMonitorRecord {
	t.Helper()
	raw, err := h.inbox.LoadReceiveMonitor(context.Background(), "telegram", "local")
	if err != nil {
		t.Fatal(err)
	}
	var value telegramMonitorRecord
	if json.Unmarshal(raw, &value) != nil {
		t.Fatal("invalid durable monitor record")
	}
	return value
}

func syntheticTelegramIncrement(peer, cursor string) telegramuser.IncrementalBatch {
	return telegramuser.IncrementalBatch{Batch: telegramuser.Batch{Identity: telegramuser.Identity{AccountID: "123", Name: "Synthetic owner", ReadOnly: true}, PeerID: peer, PeerName: "Synthetic friend", Messages: []telegramuser.Message{{ExternalID: "telegram:user:" + peer + ":11", Body: "Synthetic new message", Direction: "incoming", OccurredAt: "2026-01-01T00:00:00Z"}}, Skipped: 1}, Cursor: cursor}
}

func TestTelegramMonitorDefaultAuthorizationAndValidation(t *testing.T) {
	h := newEmailTestHandler(t)
	s, _, _ := bindIncrementalTelegram(t, h)
	for _, method := range []string{"GET", "POST"} {
		if w := emailTestRequest(h, method, telegramMonitorPath, `{"enabled":false}`, false); w.Code != 401 {
			t.Fatal(w.Code)
		}
	}
	w := emailTestRequest(h, "GET", telegramMonitorPath, "", true)
	var status TelegramMonitorStatus
	if json.Unmarshal(w.Body.Bytes(), &status) != nil || w.Code != 200 || status.Enabled || status.IntervalSeconds != 60 || status.PeerIDs == nil || !status.ReadOnly || status.SendingEnabled || s.calls.Load() != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	prepareTelegram(t, h)
	for _, body := range []string{`{}`, `null`, `{"enabled":null}`, `{"enabled":true}`, `{"enabled":true,"expected_account_id":"123","peer_ids":[]}`, `{"enabled":true,"expected_account_id":"123","peer_ids":["456","456"]}`, `{"enabled":true,"expected_account_id":"123","peer_ids":["456"],"interval_seconds":29}`, `{"enabled":true,"expected_account_id":"123","peer_ids":["456"],"interval_seconds":901}`, `{"enabled":false,"interval_seconds":null}`, `{"enabled":false,"peer_ids":null}`, `{"enabled":false,"key":"secret"}`, `{"enabled":false,"enabled":true}`} {
		if w := emailTestRequest(h, "POST", telegramMonitorPath, body, true); w.Code != 400 {
			t.Fatal("invalid subscription accepted", body, w.Code, w.Body.String())
		}
	}
	if w := emailTestRequest(h, "POST", telegramMonitorPath, `{"enabled":true,"expected_account_id":"789","peer_ids":["456"]}`, true); w.Code != 409 {
		t.Fatal("stale identity accepted", w.Code)
	}
	if w := emailTestRequest(h, "POST", telegramMonitorPath, `{"enabled":true,"expected_account_id":"123","peer_ids":["999"]}`, true); w.Code != 400 {
		t.Fatal("unselected peer accepted", w.Code)
	}
	if s.calls.Load() != 2 {
		t.Fatal("monitor metadata/invalid requests contacted helper")
	}
}

func TestTelegramMonitorBaselineIncrementalDurabilityAndSkippedRecords(t *testing.T) {
	h := newEmailTestHandler(t)
	s, ticks, cycles := bindIncrementalTelegram(t, h)
	prepareTelegram(t, h)
	enableTelegramMonitor(t, h, "456")
	telegramMonitorCycle(t, cycles)
	first := telegramSavedMonitor(t, h)
	if first.Cursors["456"] != "10" || first.LastSuccessAt != nil {
		t.Fatal("initial baseline imported history or claimed a scan", first)
	}
	descriptors := connector.Planned()
	h.applyTelegram(descriptors)
	for _, item := range descriptors {
		if item.ID == "telegram-user" && (!item.Capabilities.ReceiveText || !item.Capabilities.HistoryImport || item.Status != "identity_verified") {
			t.Fatal("authorized receiver lost capability or advertised a new online state", item)
		}
	}
	page, _ := h.inbox.Conversations(store.Filter{Platform: "telegram"})
	if len(page.Items) != 0 {
		t.Fatal("baseline created old history")
	}
	s.mu.Lock()
	s.batches["456"] = syntheticTelegramIncrement("456", "12")
	s.mu.Unlock()
	ticks <- struct{}{}
	telegramMonitorCycle(t, cycles)
	saved := telegramSavedMonitor(t, h)
	if saved.Cursors["456"] != "12" || saved.LastSuccessAt == nil || saved.SkippedCount != 1 {
		t.Fatal("incremental cursor/count missing", saved)
	}
	page, _ = h.inbox.Conversations(store.Filter{Platform: "telegram"})
	if len(page.Items) != 1 || page.Items[0].AccountID != "123" {
		t.Fatal("new messages not account scoped")
	}
	s.mu.Lock()
	delete(s.batches, "456")
	s.mu.Unlock()
	ticks <- struct{}{}
	telegramMonitorCycle(t, cycles)
	s.mu.Lock()
	after := append([]string{}, s.afters["456"]...)
	s.mu.Unlock()
	if strings.Join(after, ",") != "10,12" {
		t.Fatal("cursor not incremental", after)
	}
	if w := emailTestRequest(h, "POST", telegramMonitorPath, `{"enabled":false}`, true); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if state := telegramSavedMonitor(t, h); state.Enabled || state.Cursors["456"] != "12" {
		t.Fatal("pause lost cursor", state)
	}
	h.applyTelegram(descriptors)
	for _, item := range descriptors {
		if item.ID == "telegram-user" && (item.Capabilities.ReceiveText || !item.Capabilities.HistoryImport) {
			t.Fatal("pause left receiving advertised or removed history", item)
		}
	}
}

func TestTelegramMonitorPauseCancelsBeforeImport(t *testing.T) {
	h := newEmailTestHandler(t)
	s, ticks, cycles := bindIncrementalTelegram(t, h)
	prepareTelegram(t, h)
	enableTelegramMonitor(t, h, "456")
	telegramMonitorCycle(t, cycles)
	s.mu.Lock()
	s.block = true
	s.entered = make(chan struct{}, 1)
	entered := s.entered
	s.mu.Unlock()
	ticks <- struct{}{}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("scan never started")
	}
	if w := emailTestRequest(h, "POST", telegramMonitorPath, `{"enabled":false}`, true); w.Code != 200 {
		t.Fatal("pause failed", w.Code, w.Body.String())
	}
	if got := telegramSavedMonitor(t, h); got.Enabled || got.Cursors["456"] != "10" {
		t.Fatal("canceled read advanced cursor", got)
	}
	page, _ := h.inbox.Conversations(store.Filter{Platform: "telegram"})
	if len(page.Items) != 0 {
		t.Fatal("late write after pause")
	}
}

func TestTelegramMonitorImportConflictDoesNotAdvanceCursor(t *testing.T) {
	h := newEmailTestHandler(t)
	s, ticks, cycles := bindIncrementalTelegram(t, h)
	prepareTelegram(t, h)
	enableTelegramMonitor(t, h, "456")
	telegramMonitorCycle(t, cycles)
	_, err := h.inbox.Import(store.ImportRequest{Platform: "telegram", AccountID: "123", Messages: []store.ImportMessage{{ExternalID: "telegram:user:456:11", PeerID: "456", PeerName: "Synthetic friend", Direction: "incoming", Body: "Existing different body", OccurredAt: "2026-01-01T00:00:00Z"}}})
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.batches["456"] = syntheticTelegramIncrement("456", "12")
	s.mu.Unlock()
	ticks <- struct{}{}
	telegramMonitorCycle(t, cycles)
	if saved := telegramSavedMonitor(t, h); saved.Cursors["456"] != "10" || saved.LastSuccessAt != nil {
		t.Fatal("failed import advanced cursor", saved)
	}
	if status := h.telegram.monitorSnapshot(); status.State != "error" || status.LastErrorCode == nil || *status.LastErrorCode != "STORE_UNAVAILABLE" {
		t.Fatal(status)
	}
}

func TestTelegramMonitorRestoreContinuesFromDurableCursor(t *testing.T) {
	h := newEmailTestHandler(t)
	s, ticks, cycles := bindIncrementalTelegram(t, h)
	prepareTelegram(t, h)
	enableTelegramMonitor(t, h, "456")
	telegramMonitorCycle(t, cycles)
	s.mu.Lock()
	s.batches["456"] = syntheticTelegramIncrement("456", "12")
	s.mu.Unlock()
	ticks <- struct{}{}
	telegramMonitorCycle(t, cycles)
	h.closeTelegramMonitor()
	connection, err := newTelegramConnection(telegramuser.Config{})
	if err != nil {
		t.Fatal(err)
	}
	restored := &handler{telegram: connection, inbox: h.inbox}
	s2, _, cycles2 := bindIncrementalTelegram(t, restored)
	t.Cleanup(restored.closeTelegramMonitor)
	if err := restored.restoreTelegramMonitor(); err != nil {
		t.Fatal(err)
	}
	telegramMonitorCycle(t, cycles2)
	s2.mu.Lock()
	baselines := s2.baselines["456"]
	afters := append([]string{}, s2.afters["456"]...)
	s2.mu.Unlock()
	if baselines != 0 || strings.Join(afters, ",") != "12" {
		t.Fatal("restart rebaselined or reread old history", baselines, afters)
	}
}

func TestTelegramMonitorAccountChangeBlocksWithoutMessageImport(t *testing.T) {
	h := newEmailTestHandler(t)
	s, ticks, cycles := bindIncrementalTelegram(t, h)
	prepareTelegram(t, h)
	enableTelegramMonitor(t, h, "456")
	telegramMonitorCycle(t, cycles)
	s.mu.Lock()
	s.actual = "789"
	s.mu.Unlock()
	ticks <- struct{}{}
	deadline := time.Now().Add(5 * time.Second)
	for h.telegram.monitorSnapshot().State != "blocked" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	status := h.telegram.monitorSnapshot()
	if status.State != "blocked" || status.LastErrorCode == nil || *status.LastErrorCode != "TELEGRAM_IDENTITY_MISMATCH" {
		t.Fatal(status)
	}
	items := connector.Planned()
	h.applyTelegram(items)
	for _, item := range items {
		if item.ID == "telegram-user" && item.Capabilities.ReceiveText {
			t.Fatal("blocked monitor advertised receiving")
		}
	}
	if saved := telegramSavedMonitor(t, h); saved.Cursors["456"] != "10" || saved.ExpectedAccountID != "123" {
		t.Fatal("account change promoted authority", saved)
	}
	page, _ := h.inbox.Conversations(store.Filter{Platform: "telegram"})
	if len(page.Items) != 0 {
		t.Fatal("replacement account messages imported")
	}
}

func TestTelegramMonitorFloodWaitOverridesEarlierPeerError(t *testing.T) {
	h := newEmailTestHandler(t)
	s, ticks, cycles := bindIncrementalTelegram(t, h)
	prepareTelegram(t, h)
	enableTelegramMonitor(t, h, "456", "457")
	telegramMonitorCycle(t, cycles)
	s.mu.Lock()
	s.failures["456"] = &telegramuser.Error{Code: "network_error"}
	s.failures["457"] = &telegramuser.Error{Code: "flood_wait", RetryAfter: 180}
	s.mu.Unlock()
	ticks <- struct{}{}
	if wait := telegramMonitorCycle(t, cycles); wait < 180*time.Second {
		t.Fatal("flood wait ignored", wait)
	}
	status := h.telegram.monitorSnapshot()
	if status.State != "flood_wait" || status.LastErrorCode == nil || *status.LastErrorCode != "TELEGRAM_FLOOD_WAIT" {
		t.Fatal(status)
	}
	if w := emailTestRequest(h, "POST", telegramMonitorPath, `{"enabled":false}`, true); w.Code != 200 {
		t.Fatal("flood sleep not cancellable", w.Code)
	}
	if saved := telegramSavedMonitor(t, h); saved.RetryNotBefore == nil {
		t.Fatal("pause lost flood deadline")
	}
	if w := emailTestRequest(h, "POST", telegramMonitorPath, `{"enabled":false,"expected_account_id":"789","peer_ids":[]}`, true); w.Code != 400 {
		t.Fatal("pause changed account and cleared flood deadline", w.Code)
	}
	enableTelegramMonitor(t, h, "456", "457")
	if wait := telegramMonitorCycle(t, cycles); wait < 175*time.Second {
		t.Fatal("settings bypassed flood deadline", wait)
	}
	h.closeTelegramMonitor()
	connection, err := newTelegramConnection(telegramuser.Config{ProjectRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	restored := &handler{telegram: connection, inbox: h.inbox}
	s2, _, cycles2 := bindIncrementalTelegram(t, restored)
	t.Cleanup(restored.closeTelegramMonitor)
	if err := restored.restoreTelegramMonitor(); err != nil {
		t.Fatal(err)
	}
	if wait := telegramMonitorCycle(t, cycles2); wait < 170*time.Second {
		t.Fatal("restart bypassed flood deadline", wait)
	}
	s2.mu.Lock()
	calls := len(s2.afters) + len(s2.baselines)
	s2.mu.Unlock()
	if calls != 0 {
		t.Fatal("helper called before persisted flood deadline")
	}
}

func TestTelegramMonitorSameAccountSelectionOnlyBaselinesNewPeers(t *testing.T) {
	h := newEmailTestHandler(t)
	s, _, cycles := bindIncrementalTelegram(t, h)
	prepareTelegram(t, h)
	enableTelegramMonitor(t, h, "456")
	telegramMonitorCycle(t, cycles)
	enableTelegramMonitor(t, h, "456", "457")
	telegramMonitorCycle(t, cycles)
	s.mu.Lock()
	first, added := s.baselines["456"], s.baselines["457"]
	afters := append([]string{}, s.afters["456"]...)
	s.mu.Unlock()
	if first != 1 || added != 1 || strings.Join(afters, ",") != "10" {
		t.Fatal("selection change lost old cursor", first, added, afters)
	}
}

func TestTelegramMonitorCorruptSettingsAndCanceledUpdate(t *testing.T) {
	h := newEmailTestHandler(t)
	bindIncrementalTelegram(t, h)
	if err := h.inbox.SaveReceiveMonitor(context.Background(), "telegram", "local", json.RawMessage(`{"version":999,"enabled":true}`)); err != nil {
		t.Fatal(err)
	}
	if err := h.restoreTelegramMonitor(); !errors.Is(err, store.ErrUnavailable) {
		t.Fatal("corrupt saved authority accepted", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("POST", "http://127.0.0.1:17890"+telegramMonitorPath, strings.NewReader(`{"enabled":false}`)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.serveTelegramMonitor(w, r, "synthetic")
	if w.Code != 503 {
		t.Fatal("canceled config update committed", w.Code, w.Body.String())
	}
}
