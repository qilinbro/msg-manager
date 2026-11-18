package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qilinbro/msg-manager/internal/config"
	"github.com/qilinbro/msg-manager/internal/connector/mailbox"
	"github.com/qilinbro/msg-manager/internal/store"
)

const emailMonitorPath = "/api/v1/connections/email-imap/monitor"

type monitorMailbox struct {
	probes    atomic.Int32
	reads     atomic.Int32
	mu        sync.Mutex
	info      mailbox.Info
	delta     *mailbox.Delta
	err       error
	entered   chan struct{}
	exited    chan struct{}
	block     bool
	makeDelta func(mailbox.Cursor) mailbox.Delta
}

func (s *monitorMailbox) Fetch(context.Context) (mailbox.Batch, error) { return validMailBatch(), nil }
func (s *monitorMailbox) Probe(context.Context) (mailbox.Info, error) {
	s.probes.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.info, s.err
}
func (s *monitorMailbox) FetchAfter(ctx context.Context, cursor mailbox.Cursor) (mailbox.Delta, error) {
	s.reads.Add(1)
	if s.entered != nil {
		select {
		case s.entered <- struct{}{}:
		default:
		}
	}
	if s.block {
		<-ctx.Done()
		if s.exited != nil {
			close(s.exited)
		}
		return mailbox.Delta{}, ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return mailbox.Delta{}, s.err
	}
	if s.makeDelta != nil {
		return s.makeDelta(cursor), nil
	}
	if s.delta != nil {
		return *s.delta, nil
	}
	info := s.info
	info.UIDNext = cursor.NextUID
	return mailbox.Delta{Batch: mailbox.Batch{Info: info, Mails: []mailbox.Mail{}}, Next: cursor, SkippedUIDs: []mailbox.SkippedUID{}}, nil
}
func bindMonitorMailbox(h *handler) *monitorMailbox {
	account, folder := "receiver@example.test", "INBOX"
	h.email.accountID = "imap-synthetic-account"
	h.email.status = EmailStatus{Configured: true, State: "ready", Account: &account, Folder: &folder}
	s := &monitorMailbox{info: mailbox.Info{AccountID: account, Folder: folder, UIDValidity: 42, UIDNext: 10, Messages: 9, ReadOnly: true}}
	h.email.client = s
	return s
}
func emailTestCheckpoint() emailCheckpoint {
	return emailCheckpoint{Enabled: true, IntervalSeconds: 60, Cursor: mailbox.Cursor{UIDValidity: 42, NextUID: 10}, Gaps: []mailbox.SkippedUID{}}
}
func emailNewDelta() mailbox.Delta {
	base := validMailBatch()
	base.Info.UIDNext = 11
	base.Mails[0].UID = 10
	base.Skipped = 0
	base.Truncated = false
	return mailbox.Delta{Batch: base, Next: mailbox.Cursor{UIDValidity: 42, NextUID: 11}, SkippedUIDs: []mailbox.SkippedUID{}}
}
func loadEmailCheckpoint(t *testing.T, h *handler) emailCheckpoint {
	t.Helper()
	raw, err := h.inbox.LoadReceiveMonitor(context.Background(), "email", h.email.monitorBinding())
	if err != nil {
		t.Fatal(err)
	}
	var p emailCheckpoint
	if json.Unmarshal(raw, &p) != nil {
		t.Fatal("checkpoint decode")
	}
	return p
}

func TestEmailMonitorNeedsExplicitAuthorizationAndFreshFutureOnlyBaseline(t *testing.T) {
	h := newEmailTestHandler(t)
	s := bindMonitorMailbox(h)
	for _, method := range []string{"GET", "POST"} {
		if w := emailTestRequest(h, method, emailMonitorPath, `{"enabled":true}`, false); w.Code != 401 {
			t.Fatal("unauthorized monitor", w.Code)
		}
	}
	if w := emailTestRequest(h, "GET", emailMonitorPath, "", true); w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":false`) || s.probes.Load() != 0 || s.reads.Load() != 0 {
		t.Fatal("GET did network", w.Code)
	}
	for _, body := range []string{`{}`, `null`, `{"enabled":true,"interval_seconds":29}`, `{"enabled":true,"interval_seconds":901}`, `{"enabled":true,"password":"synthetic"}`, `{"enabled":true,"folder":"Other"}`, `{"enabled":true,"enabled":false}`} {
		if w := emailTestRequest(h, "POST", emailMonitorPath, body, true); w.Code != 400 {
			t.Fatal("unsafe monitor request", w.Code, body)
		}
	}
	if s.probes.Load() != 0 || s.reads.Load() != 0 {
		t.Fatal("bad input started mailbox")
	}
	w := emailTestRequest(h, "POST", emailMonitorPath, `{"enabled":true,"interval_seconds":120}`, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"baseline_mode":"future_only"`) || s.probes.Load() != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = emailTestRequest(h, "POST", emailMonitorPath, `{"enabled":false}`, true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	p := loadEmailCheckpoint(t, h)
	if p.Enabled || p.Cursor.NextUID != 10 || p.Cursor.UIDValidity != 42 || p.IntervalSeconds != 120 {
		t.Fatal("baseline/pause checkpoint", p)
	}
	page, _ := h.inbox.Conversations(store.Filter{Platform: "email"})
	if len(page.Items) != 0 {
		t.Fatal("first enable imported old snapshot")
	}
	before := s.reads.Load()
	time.Sleep(20 * time.Millisecond)
	if s.reads.Load() != before {
		t.Fatal("paused monitor kept polling")
	}
	// A deliberate re-enable of an ordinary paused monitor resumes its saved
	// cursor; it must not skip arrivals by re-probing a new high-water mark.
	w = emailTestRequest(h, "POST", emailMonitorPath, `{"enabled":true}`, true)
	if w.Code != 200 || s.probes.Load() != 1 {
		t.Fatal("resume silently rebaselined")
	}
	_ = emailTestRequest(h, "POST", emailMonitorPath, `{"enabled":false}`, true)
}

func TestEmailMonitorTickImportsBeforeCheckpointAndManualReplayHasSameIdentity(t *testing.T) {
	h := newEmailTestHandler(t)
	s := bindMonitorMailbox(h)
	d := emailNewDelta()
	s.delta = &d
	p := emailTestCheckpoint()
	if h.saveEmailCheckpoint(context.Background(), p) != nil {
		t.Fatal("initial save")
	}
	if !h.emailMonitorTick(context.Background(), &p) {
		t.Fatal("tick failed")
	}
	saved := loadEmailCheckpoint(t, h)
	if saved.Cursor.NextUID != 11 || saved.LastSuccessAt == nil || saved.LastErrorCode != nil {
		t.Fatal("watermark missing", saved)
	}
	page, _ := h.inbox.Conversations(store.Filter{Platform: "email"})
	if len(page.Items) != 1 {
		t.Fatal("mail not imported")
	}
	read := true
	_ = h.inbox.UpdateConversation(page.Items[0].ID, store.ConversationPatch{Read: &read})
	req, _ := emailImportBatch(h.email.accountID, *h.email.status.Account, *h.email.status.Folder, d.Batch)
	r, err := h.inbox.ImportIMAPContext(context.Background(), req)
	if err != nil || r.Duplicates != 1 || r.Inserted != 0 {
		t.Fatal("manual and background IDs differ", r, err)
	}
	page, _ = h.inbox.Conversations(store.Filter{Platform: "email", Unread: true})
	if len(page.Items) != 0 {
		t.Fatal("replay reset local read")
	}
}

func TestEmailMonitorPersistsSkipsAndRejectsUnreportedProgress(t *testing.T) {
	h := newEmailTestHandler(t)
	s := bindMonitorMailbox(h)
	d := emailNewDelta()
	d.Mails = []mailbox.Mail{}
	d.Skipped = 1
	d.SkippedUIDs = []mailbox.SkippedUID{{UID: 10, Reason: "oversized"}}
	s.delta = &d
	p := emailTestCheckpoint()
	if !h.emailMonitorTick(context.Background(), &p) {
		t.Fatal("handled skip failed")
	}
	saved := loadEmailCheckpoint(t, h)
	if saved.Cursor.NextUID != 11 || saved.Skipped != 1 || len(saved.Gaps) != 1 || saved.Gaps[0].UID != 10 {
		t.Fatal("cursor advanced without durable gap", saved)
	}
	d.SkippedUIDs = []mailbox.SkippedUID{}
	d.Skipped = 0
	s.delta = &d
	p = emailTestCheckpoint()
	if h.emailMonitorTick(context.Background(), &p) {
		t.Fatal("unreported gap advanced")
	}
	saved = loadEmailCheckpoint(t, h)
	if saved.Cursor.NextUID != 10 || saved.Enabled || saved.LastErrorCode == nil || *saved.LastErrorCode != "EMAIL_PROTOCOL_ERROR" {
		t.Fatal("unreported progress not stopped", saved)
	}
}

func TestEmailMonitorPauseCancelsAndJoinsOutstandingFetch(t *testing.T) {
	h := newEmailTestHandler(t)
	s := bindMonitorMailbox(h)
	s.block = true
	s.entered = make(chan struct{}, 1)
	s.exited = make(chan struct{})
	w := emailTestRequest(h, "POST", emailMonitorPath, `{"enabled":true}`, true)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	select {
	case <-s.entered:
	case <-time.After(time.Second):
		t.Fatal("background never started")
	}
	w = emailTestRequest(h, "POST", emailMonitorPath, `{"enabled":false}`, true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	select {
	case <-s.exited:
	default:
		t.Fatal("pause returned before source stopped")
	}
	if p := loadEmailCheckpoint(t, h); p.Enabled || p.Cursor.NextUID != 10 {
		t.Fatal("pause lost cursor")
	}
	if page, _ := h.inbox.Conversations(store.Filter{Platform: "email"}); len(page.Items) != 0 {
		t.Fatal("cancelled source imported")
	}
}

func TestEmailMonitorUIDValidityChangeStopsUntilExplicitRebaseline(t *testing.T) {
	h := newEmailTestHandler(t)
	s := bindMonitorMailbox(h)
	s.err = &mailbox.Error{Kind: mailbox.UIDValidityChanged}
	p := emailTestCheckpoint()
	if h.emailMonitorTick(context.Background(), &p) {
		t.Fatal("generation change kept worker running")
	}
	saved := loadEmailCheckpoint(t, h)
	if saved.Enabled || saved.Cursor.NextUID != 10 || saved.LastErrorCode == nil || *saved.LastErrorCode != "EMAIL_UIDVALIDITY_CHANGED" {
		t.Fatal("generation failure did not preserve old watermark", saved)
	}
	s.err = nil
	s.info.UIDValidity = 43
	s.info.UIDNext = 100
	w := emailTestRequest(h, "POST", emailMonitorPath, `{"enabled":true}`, true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	_ = emailTestRequest(h, "POST", emailMonitorPath, `{"enabled":false}`, true)
	saved = loadEmailCheckpoint(t, h)
	if saved.Cursor.UIDValidity != 43 || saved.Cursor.NextUID != 100 || s.probes.Load() != 1 {
		t.Fatal("explicit new generation baseline failed", saved)
	}
}

func emailFixtureSQL(t *testing.T, path string) *sql.DB {
	t.Helper()
	uri := filepath.ToSlash(path)
	if runtime.GOOS == "windows" {
		uri = "/" + uri
	}
	u := url.URL{Scheme: "file", Path: uri}
	q := url.Values{}
	q.Set("_pragma_key", "x'"+strings.Repeat("ab", 32)+"'")
	u.RawQuery = q.Encode()
	db, err := sql.Open("social-sqlcipher", u.String())
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}
func TestEmailMonitorCheckpointFailureReplaysSafelyAndRestoresOnlyMatchingBinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoint-test.db")
	handlerValue, err := NewHandler(config.Config{Addr: config.DefaultAddr, APIToken: emailTestToken, DatabasePath: path, DatabaseKey: strings.Repeat("ab", 32)})
	if err != nil {
		t.Fatal(err)
	}
	h := handlerValue.(*handler)
	t.Cleanup(func() { _ = h.Close() })
	s := bindMonitorMailbox(h)
	d := emailNewDelta()
	s.delta = &d
	p := emailTestCheckpoint()
	if h.saveEmailCheckpoint(context.Background(), p) != nil {
		t.Fatal("save initial")
	}
	db := emailFixtureSQL(t, path)
	if _, err := db.Exec(`CREATE TRIGGER reject_email_checkpoint BEFORE INSERT ON receive_monitors WHEN NEW.scope='email' BEGIN SELECT RAISE(ABORT,'synthetic-save-failure'); END`); err != nil {
		t.Fatal(err)
	}
	if h.emailMonitorTick(context.Background(), &p) {
		t.Fatal("checkpoint failure continued")
	}
	if saved := loadEmailCheckpoint(t, h); saved.Cursor.NextUID != 10 {
		t.Fatal("failed checkpoint advanced")
	}
	page, _ := h.inbox.Conversations(store.Filter{Platform: "email"})
	if len(page.Items) != 1 {
		t.Fatal("import was not committed before watermark attempt")
	}
	if _, err := db.Exec(`DROP TRIGGER reject_email_checkpoint`); err != nil {
		t.Fatal(err)
	}
	p = loadEmailCheckpoint(t, h)
	if !h.emailMonitorTick(context.Background(), &p) {
		t.Fatal("safe recovery replay failed")
	}
	page, _ = h.inbox.Conversations(store.Filter{Platform: "email"})
	if len(page.Items) != 1 {
		t.Fatal("checkpoint replay duplicated data")
	}
	// A different configured account must not inherit saved receive permission.
	h.email.accountID = "imap-different-account"
	if err := h.restoreEmailMonitor(); err != nil {
		t.Fatal(err)
	}
	if h.email.cancel != nil {
		t.Fatal("new account inherited old worker")
	}
}

func TestEmailMonitorRecoveryIsBoundedAndMatchingRestartUsesSavedCursor(t *testing.T) {
	h := newEmailTestHandler(t)
	s := bindMonitorMailbox(h)
	s.makeDelta = func(cursor mailbox.Cursor) mailbox.Delta {
		info := s.info
		info.UIDNext = cursor.NextUID + 2
		return mailbox.Delta{Batch: mailbox.Batch{Info: info, Mails: []mailbox.Mail{}, Truncated: true}, Next: mailbox.Cursor{UIDValidity: cursor.UIDValidity, NextUID: cursor.NextUID + 1}, MissingUIDCount: 1, SkippedUIDs: []mailbox.SkippedUID{}}
	}
	p := emailTestCheckpoint()
	if !h.emailMonitorTick(context.Background(), &p) || s.reads.Load() != 3 || p.Cursor.NextUID != 13 || p.MissingUIDCount != 3 {
		t.Fatal("unbounded recovery or silent holes", s.reads.Load(), p)
	}
	h.email.mu.RLock()
	limited := h.email.monitor.Limited
	h.email.mu.RUnlock()
	if !limited {
		t.Fatal("backlog not exposed")
	}

	path := filepath.Join(t.TempDir(), "restart-email.db")
	create := func() *handler {
		v, err := NewHandler(config.Config{Addr: config.DefaultAddr, APIToken: emailTestToken, DatabasePath: path, DatabaseKey: strings.Repeat("ab", 32)})
		if err != nil {
			t.Fatal(err)
		}
		return v.(*handler)
	}
	first := create()
	bindMonitorMailbox(first)
	p = emailTestCheckpoint()
	if err := first.saveEmailCheckpoint(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second := create()
	t.Cleanup(func() { _ = second.Close() })
	source := bindMonitorMailbox(second)
	d := emailNewDelta()
	source.delta = &d
	if err := second.restoreEmailMonitor(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		second.email.mu.RLock()
		successful := second.email.monitor.LastSuccessAt != nil
		second.email.mu.RUnlock()
		if successful {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("saved monitor did not resume")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if w := emailTestRequest(second, "POST", emailMonitorPath, `{"enabled":false}`, true); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if source.probes.Load() != 0 || source.reads.Load() != 1 || loadEmailCheckpoint(t, second).Cursor.NextUID != 11 {
		t.Fatal("restart rebaselined instead of recovering saved cursor")
	}
	page, _ := second.inbox.Conversations(store.Filter{Platform: "email"})
	if len(page.Items) != 1 {
		t.Fatal("restart did not ingest missed new mail")
	}
	// Loading the disabled record is cache-only and does not restart polling.
	before := source.reads.Load()
	if err := second.restoreEmailMonitor(); err != nil {
		t.Fatal(err)
	}
	if source.reads.Load() != before {
		t.Fatal("paused record contacted mailbox on restore")
	}
}
