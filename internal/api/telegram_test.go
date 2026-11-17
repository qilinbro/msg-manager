package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/qilinbro/msg-manager/internal/config"
	"github.com/qilinbro/msg-manager/internal/connector/telegramuser"
	"github.com/qilinbro/msg-manager/internal/store"
)

type stubTelegram struct {
	calls    atomic.Int32
	identity telegramuser.Identity
	dialogs  telegramuser.Dialogs
	batch    telegramuser.Batch
	err      error
	entered  chan struct{}
	release  chan struct{}
}

func (s *stubTelegram) Metadata() telegramuser.Metadata {
	return telegramuser.Metadata{Configured: true, HelperAvailable: true}
}
func (s *stubTelegram) Probe(context.Context) (telegramuser.Identity, error) {
	s.calls.Add(1)
	return s.identity, s.err
}
func (s *stubTelegram) Dialogs(context.Context) (telegramuser.Dialogs, error) {
	s.calls.Add(1)
	return s.dialogs, s.err
}
func (s *stubTelegram) Read(ctx context.Context, _ string, _ int) (telegramuser.Batch, error) {
	s.calls.Add(1)
	if s.entered != nil {
		s.entered <- struct{}{}
		select {
		case <-s.release:
		case <-ctx.Done():
			return telegramuser.Batch{}, ctx.Err()
		}
	}
	return s.batch, s.err
}
func bindTelegram(h *handler) *stubTelegram {
	id := telegramuser.Identity{AccountID: "123", Name: "合成本人账号", ReadOnly: true}
	s := &stubTelegram{identity: id, dialogs: telegramuser.Dialogs{Identity: id, Items: []telegramuser.Dialog{{PeerID: "456", Name: "合成好友"}}, Limited: true}, batch: telegramuser.Batch{Identity: id, PeerID: "456", PeerName: "合成好友", Messages: []telegramuser.Message{{ExternalID: "telegram:user:456:1", Body: "测试正文", Direction: "incoming", OccurredAt: "2026-01-01T00:00:00Z"}}, Limited: true}}
	h.telegram.client = s
	return s
}
func prepareTelegram(t *testing.T, h *handler) {
	t.Helper()
	for _, entry := range []struct{ method, path string }{{"POST", "/verify"}, {"GET", "/dialogs"}} {
		w := emailTestRequest(h, entry.method, telegramPath+entry.path, "", true)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func TestTelegramStatusAndAuthorizationNeverStartHelper(t *testing.T) {
	h := newEmailTestHandler(t)
	s := bindTelegram(h)
	for _, entry := range []struct{ method, path, body string }{{"GET", "/status", ""}, {"POST", "/verify", ""}, {"GET", "/dialogs", ""}, {"POST", "/sync", `{"peer_id":"456","limit":10,"expected_account_id":"123"}`}} {
		if w := emailTestRequest(h, entry.method, telegramPath+entry.path, entry.body, false); w.Code != 401 {
			t.Fatal(w.Code)
		}
	}
	for _, path := range []string{telegramPath + "/status", "/api/v1/system", "/api/v1/connections", "/api/v1/inbox"} {
		if w := emailTestRequest(h, "GET", path, "", true); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if s.calls.Load() != 0 {
		t.Fatal("status or unauthorized request started network operation")
	}
	for _, body := range []string{`{}`, `{"api_hash":"synthetic-secret"}`} {
		if w := emailTestRequest(h, "POST", telegramPath+"/verify", body, true); w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	if w := emailTestRequest(h, "GET", telegramPath+"/dialogs", "", true); w.Code != 503 {
		t.Fatal("dialogs before verify", w.Code)
	}
	for _, path := range []string{"/verify", "/dialogs", "/sync"} {
		method := "POST"
		if path == "/dialogs" {
			method = "GET"
		}
		if w := emailTestRequest(h, method, telegramPath+path+"?api_hash=synthetic-secret", "", true); w.Code != 400 {
			t.Fatal("query accepted", w.Code)
		}
	}
	if s.calls.Load() != 0 {
		t.Fatal("invalid input launched helper")
	}
	request := httptest.NewRequest("POST", "http://"+config.DefaultAddr+telegramPath+"/verify", nil)
	request.Header.Set("Origin", "https://attacker.test")
	request.Header.Set("Authorization", "Bearer "+emailTestToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request)
	if w.Code != 403 || s.calls.Load() != 0 {
		t.Fatal("foreign origin launched helper")
	}
	request = httptest.NewRequest("OPTIONS", "http://"+config.DefaultAddr+telegramPath+"/verify", nil)
	request.Header.Set("Origin", "http://127.0.0.1:1420")
	request.Header.Set("Access-Control-Request-Method", "POST")
	request.Header.Set("Access-Control-Request-Headers", "authorization")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, request)
	if w.Code != 204 {
		t.Fatal("POST verify preflight denied", w.Code)
	}
	if w := emailTestRequest(h, "GET", "/api/v1/connections/telegram-bot/verify", "", true); w.Code != 404 {
		t.Fatal("retired bot alias enabled")
	}
}

func TestTelegramExplicitPrivateReadImportsAndDeduplicates(t *testing.T) {
	h := newEmailTestHandler(t)
	s := bindTelegram(h)
	prepareTelegram(t, h)
	for _, body := range []string{`null`, `[]`, `{}`, `{"peer_id":"456","limit":10}`, `{"peer_id":"456","limit":10,"expected_account_id":""}`, `{"peer_id":"456","limit":10,"expected_account_id":"0123"}`, `{"peer_id":"456","limit":0}`, `{"peer_id":"456","limit":101}`, `{"peer_id":"0456","limit":10}`, `{"peer_id":"456","limit":10,"api_hash":"secret"}`, `{"peer_id":"456","peer_id":"789","limit":10}`} {
		if w := emailTestRequest(h, "POST", telegramPath+"/sync", body, true); w.Code != 400 {
			t.Fatal("invalid input accepted", w.Code, w.Body.String())
		}
	}
	if w := emailTestRequest(h, "POST", telegramPath+"/sync", `{"peer_id":"789","limit":10,"expected_account_id":"123"}`, true); w.Code != 400 {
		t.Fatal("unselected peer")
	}
	if s.calls.Load() != 2 {
		t.Fatal("invalid sync invoked helper")
	}
	for n := 0; n < 2; n++ {
		w := emailTestRequest(h, "POST", telegramPath+"/sync", `{"peer_id":"456","limit":10,"expected_account_id":"123"}`, true)
		var result TelegramSyncResult
		if json.Unmarshal(w.Body.Bytes(), &result) != nil || w.Code != 200 || result.Inserted != 1-n || result.Duplicates != n || result.SourceKind != "telegram_user" || !result.ReadOnly || result.SendingEnabled || !result.Limited {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	page, err := h.inbox.Conversations(store.Filter{Platform: "telegram"})
	if err != nil || len(page.Items) != 1 || page.Items[0].AccountID != "123" || page.Items[0].ConversationKind != "private" {
		t.Fatal("not in private inbox", err)
	}
	read := true
	if h.inbox.UpdateConversation(page.Items[0].ID, store.ConversationPatch{Read: &read}) != nil {
		t.Fatal("local read")
	}
	if w := emailTestRequest(h, "POST", telegramPath+"/sync", `{"peer_id":"456","limit":10,"expected_account_id":"123"}`, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	unread, err := h.inbox.Conversations(store.Filter{Platform: "telegram", Unread: true})
	if err != nil || len(unread.Items) != 0 {
		t.Fatal("resync reset local read")
	}
	if w := emailTestRequest(h, "GET", "/api/v1/inbox", "", true); !strings.Contains(w.Body.String(), `"live_status":"identity_verified"`) || !strings.Contains(w.Body.String(), `"source_mode":"mixed"`) {
		t.Fatal("inbox masks personal reader", w.Body.String())
	}
	if w := emailTestRequest(h, "POST", "/api/v1/send", `{}`, true); w.Code != 501 {
		t.Fatal("read opened send route")
	}
}

func TestTelegramSyncBindsTheAccountWhoseDialogsWereSelected(t *testing.T) {
	h := newEmailTestHandler(t)
	s := bindTelegram(h)
	// Window A has loaded account 123 and selected globally stable user 456.
	prepareTelegram(t, h)
	// The owner replaces local authorization and window B verifies account 789.
	// Both accounts legitimately contain the same Telegram contact 456.
	s.identity.AccountID = "789"
	s.dialogs.AccountID = "789"
	s.batch.AccountID = "789"
	prepareTelegram(t, h)
	before := s.calls.Load()
	w := emailTestRequest(h, "POST", telegramPath+"/sync", `{"peer_id":"456","limit":10,"expected_account_id":"123"}`, true)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "TELEGRAM_IDENTITY_MISMATCH") || s.calls.Load() != before {
		t.Fatal("stale account selection reached helper", w.Code, w.Body.String())
	}
	page, _ := h.inbox.Conversations(store.Filter{Platform: "telegram"})
	if len(page.Items) != 0 {
		t.Fatal("stale window imported replacement account")
	}
	status := h.telegram.snapshot()
	if status.State != "identity_verified" || status.AccountID != "789" {
		t.Fatal("stale request revoked valid replacement account")
	}
	w = emailTestRequest(h, "POST", telegramPath+"/sync", `{"peer_id":"456","limit":10,"expected_account_id":"789"}`, true)
	if w.Code != 200 {
		t.Fatal("legitimate replacement window cannot sync", w.Code, w.Body.String())
	}
	page, _ = h.inbox.Conversations(store.Filter{Platform: "telegram"})
	if len(page.Items) != 1 || page.Items[0].AccountID != "789" {
		t.Fatal("wrong account imported")
	}
}

func TestTelegramMismatchEmptyErrorsAndBusyDoNotImport(t *testing.T) {
	for _, mutate := range []func(*stubTelegram){
		func(s *stubTelegram) { s.batch.AccountID = "789" },
		func(s *stubTelegram) { s.batch.ReadOnly = false },
		func(s *stubTelegram) { s.batch.Messages[0].ExternalID = "telegram:user:999:1" },
		func(s *stubTelegram) { s.err = errors.New("synthetic-api-hash-and-private-trace") },
	} {
		h := newEmailTestHandler(t)
		s := bindTelegram(h)
		prepareTelegram(t, h)
		mutate(s)
		w := emailTestRequest(h, "POST", telegramPath+"/sync", `{"peer_id":"456","limit":10,"expected_account_id":"123"}`, true)
		if w.Code != 503 || strings.Contains(w.Body.String(), "synthetic-api-hash") {
			t.Fatal(w.Code, w.Body.String())
		}
		page, _ := h.inbox.Conversations(store.Filter{Platform: "telegram"})
		if len(page.Items) != 0 {
			t.Fatal("failure wrote records")
		}
	}
	h := newEmailTestHandler(t)
	s := bindTelegram(h)
	prepareTelegram(t, h)
	s.batch.Messages = []telegramuser.Message{}
	s.batch.Skipped = 1
	if w := emailTestRequest(h, "POST", telegramPath+"/sync", `{"peer_id":"456","limit":10,"expected_account_id":"123"}`, true); w.Code != 200 || !strings.Contains(w.Body.String(), `"inserted":0`) {
		t.Fatal("empty batch imported", w.Code, w.Body.String())
	}
	page, _ := h.inbox.Conversations(store.Filter{Platform: "telegram"})
	if len(page.Items) != 0 {
		t.Fatal("empty batch created contacts")
	}
	s.entered = make(chan struct{}, 1)
	s.release = make(chan struct{})
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- emailTestRequest(h, "POST", telegramPath+"/sync", `{"peer_id":"456","limit":10,"expected_account_id":"123"}`, true)
	}()
	<-s.entered
	if w := emailTestRequest(h, "POST", telegramPath+"/verify", "", true); w.Code != 503 || !strings.Contains(w.Body.String(), "TELEGRAM_BUSY") {
		t.Fatal("parallel helper allowed", w.Code)
	}
	close(s.release)
	if w := <-done; w.Code != 200 {
		t.Fatal(w.Code)
	}
	s.err = &telegramuser.Error{Code: "flood_wait", RetryAfter: 60}
	w := emailTestRequest(h, "POST", telegramPath+"/sync", `{"peer_id":"456","limit":10,"expected_account_id":"123"}`, true)
	if w.Code != 503 || w.Header().Get("Retry-After") != "60" || !strings.Contains(w.Body.String(), "TELEGRAM_FLOOD_WAIT") {
		t.Fatal(w.Code, w.Body.String())
	}
}
