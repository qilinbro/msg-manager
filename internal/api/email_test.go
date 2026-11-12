package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/qilinbro/msg-manager/internal/config"
	"github.com/qilinbro/msg-manager/internal/connector/mailbox"
	"github.com/qilinbro/msg-manager/internal/store"
)

const emailTestToken = "synthetic-mail-test-local-token-20261112"

type stubMailbox struct {
	calls   atomic.Int32
	batch   mailbox.Batch
	err     error
	entered chan struct{}
	release chan struct{}
}

func (s *stubMailbox) Fetch(ctx context.Context) (mailbox.Batch, error) {
	s.calls.Add(1)
	if s.entered != nil {
		s.entered <- struct{}{}
		select {
		case <-s.release:
		case <-ctx.Done():
			return mailbox.Batch{}, ctx.Err()
		}
	}
	return s.batch, s.err
}

func newEmailTestHandler(t *testing.T) *handler {
	t.Helper()
	h, err := NewHandler(config.Config{Addr: config.DefaultAddr, APIToken: emailTestToken, DatabasePath: filepath.Join(t.TempDir(), "mail.db"), DatabaseKey: strings.Repeat("ab", 32)})
	if err != nil {
		t.Fatal(err)
	}
	actual := h.(*handler)
	t.Cleanup(func() { _ = actual.Close() })
	return actual
}

func emailTestRequest(h http.Handler, method, path, body string, authenticated bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://"+config.DefaultAddr+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://127.0.0.1:1420")
	if authenticated {
		r.Header.Set("Authorization", "Bearer "+emailTestToken)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func bindStubEmail(h *handler, stub *stubMailbox) {
	account, folder := "receiver@example.test", "INBOX"
	h.email.client = stub
	h.email.accountID = "imap-synthetic-account"
	h.email.status = EmailStatus{Configured: true, State: "ready", Account: &account, Folder: &folder}
}

func validMailBatch() mailbox.Batch {
	return mailbox.Batch{Info: mailbox.Info{AccountID: "receiver@example.test", Folder: "INBOX", UIDValidity: 42, Messages: 101, ReadOnly: true}, Mails: []mailbox.Mail{{UID: 1, Subject: "合成邮件主题", FromName: "合成发送人", FromAddress: "sender@example.test", To: []string{"receiver@example.test"}, Body: "合成正文", OccurredAt: "2026-01-01T00:00:00Z"}}, Skipped: 1, Truncated: true}
}

func TestEmailSyncIsAuthenticatedAtomicAndReadOnly(t *testing.T) {
	h := newEmailTestHandler(t)
	stub := &stubMailbox{batch: validMailBatch()}
	bindStubEmail(h, stub)
	for _, entry := range []struct{ method, path, body string }{{"GET", "/api/v1/connections/email-imap/status", ""}, {"POST", "/api/v1/connections/email-imap/sync", "{}"}} {
		if w := emailTestRequest(h, entry.method, entry.path, entry.body, false); w.Code != 401 {
			t.Fatal("unauthenticated email route", w.Code)
		}
	}
	if stub.calls.Load() != 0 {
		t.Fatal("unauthenticated request dialed email")
	}
	status := emailTestRequest(h, "GET", "/api/v1/connections/email-imap/status", "", true)
	if status.Code != 200 || stub.calls.Load() != 0 || strings.Contains(status.Body.String(), "password") {
		t.Fatal("status performed IO or leaked configuration")
	}
	for _, body := range []string{`null`, `[]`, `{"host":"attacker.example"}`, `{"password":"not-a-real-secret"}`, `{} {}`, `{"folder":"Other"}`} {
		w := emailTestRequest(h, "POST", "/api/v1/connections/email-imap/sync", body, true)
		if w.Code != 400 || stub.calls.Load() != 0 {
			t.Fatal("browser selected email target")
		}
	}
	w := emailTestRequest(h, "POST", "/api/v1/connections/email-imap/sync", "{}", true)
	var result EmailResult
	if json.Unmarshal(w.Body.Bytes(), &result) != nil || w.Code != 200 || result.Inserted != 1 || result.Skipped != 1 || !result.Limited || result.SendingEnabled {
		t.Fatal("sync result", w.Code, w.Body.String())
	}
	page, err := h.inbox.Conversations(store.Filter{Platform: "email"})
	if err != nil || len(page.Items) != 1 {
		t.Fatal("email not in unified inbox", err)
	}
	messages, err := h.inbox.Messages(page.Items[0].ID, "", 50)
	if err != nil || messages.Items[0].Email == nil || messages.Items[0].Source != "imap" {
		t.Fatal("email headers missing", err)
	}
	read := true
	if h.inbox.UpdateConversation(page.Items[0].ID, store.ConversationPatch{Read: &read}) != nil {
		t.Fatal("local read update failed")
	}
	w = emailTestRequest(h, "POST", "/api/v1/connections/email-imap/sync", "{}", true)
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	if w.Code != 200 || result.Duplicates != 1 || result.Inserted != 0 {
		t.Fatal("mail not deduplicated")
	}
	unread, err := h.inbox.Conversations(store.Filter{Platform: "email", Unread: true})
	if err != nil || len(unread.Items) != 0 {
		t.Fatal("sync reset local seen")
	}
	if w = emailTestRequest(h, "POST", "/api/v1/send", "{}", true); w.Code != 501 {
		t.Fatal("sync opened sending route")
	}
}

func TestEmailSyncErrorsBusyAndIdentityCannotPersist(t *testing.T) {
	h := newEmailTestHandler(t)
	if w := emailTestRequest(h, "POST", "/api/v1/connections/email-imap/sync", "{}", true); w.Code != 503 || !strings.Contains(w.Body.String(), "EMAIL_NOT_CONFIGURED") {
		t.Fatal("missing configuration not reported")
	}
	for _, err := range []error{&mailbox.Error{Kind: mailbox.AuthFailed}, &mailbox.Error{Kind: mailbox.TLSFailed}, errors.New("sensitive-upstream-body-not-real")} {
		stub := &stubMailbox{err: err}
		bindStubEmail(h, stub)
		w := emailTestRequest(h, "POST", "/api/v1/connections/email-imap/sync", "{}", true)
		if w.Code != 503 || strings.Contains(w.Body.String(), "sensitive-upstream") {
			t.Fatal("unsafe email error")
		}
	}
	for _, mutate := range []func(*mailbox.Batch){func(b *mailbox.Batch) { b.Info.AccountID = "other@example.test" }, func(b *mailbox.Batch) { b.Info.ReadOnly = false }, func(b *mailbox.Batch) { b.Info.UIDValidity = 0 }} {
		batch := validMailBatch()
		mutate(&batch)
		stub := &stubMailbox{batch: batch}
		bindStubEmail(h, stub)
		if w := emailTestRequest(h, "POST", "/api/v1/connections/email-imap/sync", "{}", true); w.Code != 503 {
			t.Fatal("unbound mailbox data accepted")
		}
		page, _ := h.inbox.Conversations(store.Filter{Platform: "email"})
		if len(page.Items) != 0 {
			t.Fatal("identity failure persisted records")
		}
	}
	stub := &stubMailbox{batch: validMailBatch(), entered: make(chan struct{}, 1), release: make(chan struct{})}
	bindStubEmail(h, stub)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- emailTestRequest(h, "POST", "/api/v1/connections/email-imap/sync", "{}", true) }()
	<-stub.entered
	busy := emailTestRequest(h, "POST", "/api/v1/connections/email-imap/sync", "{}", true)
	if busy.Code != 503 || !strings.Contains(busy.Body.String(), "EMAIL_BUSY") {
		t.Fatal("parallel email sync accepted")
	}
	close(stub.release)
	if w := <-done; w.Code != 200 {
		t.Fatal("first email sync failed")
	}
}
