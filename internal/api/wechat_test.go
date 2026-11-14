package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/qilinbro/msg-manager/internal/connector/wechatdb"
	"github.com/qilinbro/msg-manager/internal/store"
)

type fakeWechatReader struct {
	calls   atomic.Int32
	batch   wechatdb.Batch
	err     error
	entered chan struct{}
	release chan struct{}
}

func (f *fakeWechatReader) Probe(context.Context) (wechatdb.Info, error) {
	f.calls.Add(1)
	return f.batch.Info, f.err
}
func (f *fakeWechatReader) Sessions(context.Context) ([]wechatdb.Session, error) {
	f.calls.Add(1)
	return []wechatdb.Session{{PeerID: "wxid_peer", Name: "合成联系人"}}, f.err
}
func (f *fakeWechatReader) Read(ctx context.Context, _ string) (wechatdb.Batch, error) {
	f.calls.Add(1)
	if f.entered != nil {
		close(f.entered)
		select {
		case <-f.release:
		case <-ctx.Done():
			return wechatdb.Batch{}, ctx.Err()
		}
	}
	return f.batch, f.err
}

func bindWechat(h *handler, f *fakeWechatReader) {
	account := "wxid_synthetic"
	h.wechat.reader, h.wechat.accountID = f, account
	h.wechat.status = WechatStatus{Configured: true, State: "ready", AccountID: &account, ReadOnly: true}
}

func wechatBatch() wechatdb.Batch {
	return wechatdb.Batch{Info: wechatdb.Info{AccountID: "wxid_synthetic", ReadOnly: true}, PeerID: "wxid_peer", PeerName: "合成联系人", Limited: true, Skipped: 1,
		Messages: []wechatdb.Message{{ExternalID: "wechatdb:90071992547409931", Direction: "incoming", Body: "微信合成入站", OccurredAt: "2026-01-01T00:00:00Z"}, {ExternalID: "wechatdb:90071992547409932", Direction: "outgoing", Body: "微信合成出站", OccurredAt: "2026-01-01T00:01:00Z"}}}
}

func TestWechatReadScopeAuthenticationAndDedup(t *testing.T) {
	h := newEmailTestHandler(t)
	f := &fakeWechatReader{batch: wechatBatch()}
	bindWechat(h, f)
	const base = "/api/v1/connections/wechat-db"
	for _, route := range []struct{ method, path, body string }{{"GET", base + "/status", ""}, {"GET", base + "/sessions", ""}, {"POST", base + "/read", `{"peer_id":"wxid_peer"}`}} {
		if w := emailTestRequest(h, route.method, route.path, route.body, false); w.Code != 401 {
			t.Fatal("unauthenticated wechat access", w.Code)
		}
	}
	status := emailTestRequest(h, "GET", base+"/status", "", true)
	if status.Code != 200 || f.calls.Load() != 0 || strings.Contains(status.Body.String(), "directory") || strings.Contains(status.Body.String(), "key") {
		t.Fatal("status must be metadata only", status.Body.String())
	}
	for _, body := range []string{`null`, `{}`, `{"peer_id":"wxid_peer","key":"private"}`, `{"peer_id":"wxid_peer","account_id":"other"}`, `{"peer_id":"wxid_peer","peer_id":"other"}`, `{"peer_id":"../other"}`, `{} {}`} {
		if w := emailTestRequest(h, "POST", base+"/read", body, true); w.Code != 400 {
			t.Fatal("invalid input accepted", body, w.Code)
		}
	}
	if w := emailTestRequest(h, "GET", base+"/sessions?path=private", "", true); w.Code != 400 {
		t.Fatal("query-selected source accepted")
	}
	if f.calls.Load() != 0 {
		t.Fatal("invalid input touched source")
	}
	if w := emailTestRequest(h, "GET", base+"/sessions", "", true); w.Code != 200 || !strings.Contains(w.Body.String(), `"peer_id":"wxid_peer"`) {
		t.Fatal("sessions", w.Body.String())
	}
	for i := 0; i < 2; i++ {
		w := emailTestRequest(h, "POST", base+"/read", `{"peer_id":"wxid_peer"}`, true)
		var result WechatReadResult
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || !result.ReadOnly || result.SendingEnabled || result.Skipped != 1 || !result.Limited {
			t.Fatal("read result", w.Code, w.Body.String())
		}
		if i == 0 && result.Inserted != 2 || i == 1 && (result.Inserted != 0 || result.Duplicates != 2) {
			t.Fatal("read must deduplicate", result)
		}
	}
	page, err := h.inbox.Conversations(store.Filter{Platform: "weixin"})
	if err != nil || len(page.Items) != 1 {
		t.Fatal("wechat inbox", err)
	}
	msgs, err := h.inbox.Messages(page.Items[0].ID, "", 50)
	if err != nil || len(msgs.Items) != 2 || msgs.Items[0].Source != "wechatdb" || msgs.Items[0].Direction != "incoming" || msgs.Items[1].Direction != "outgoing" {
		t.Fatal("wechat source or direction lost", err)
	}
	if w := emailTestRequest(h, "POST", "/api/v1/send", `{}`, true); w.Code != 501 {
		t.Fatal("wechat read enabled sending")
	}
	preflight := httptest.NewRequest("OPTIONS", "http://127.0.0.1:17890"+base+"/read", nil)
	preflight.Header.Set("Origin", "http://127.0.0.1:1420")
	preflight.Header.Set("Access-Control-Request-Method", "POST")
	preflight.Header.Set("Access-Control-Request-Headers", "authorization, content-type")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, preflight)
	if w.Code != 204 {
		t.Fatal("read preflight", w.Code)
	}
}

func TestWechatMismatchErrorsAndConflictDoNotPartiallySave(t *testing.T) {
	const route = "/api/v1/connections/wechat-db/read"
	for _, change := range []func(*wechatdb.Batch){func(b *wechatdb.Batch) { b.Info.AccountID = "another_account" }, func(b *wechatdb.Batch) { b.Info.ReadOnly = false }, func(b *wechatdb.Batch) { b.PeerID = "another_peer" }} {
		h := newEmailTestHandler(t)
		b := wechatBatch()
		change(&b)
		bindWechat(h, &fakeWechatReader{batch: b})
		if w := emailTestRequest(h, "POST", route, `{"peer_id":"wxid_peer"}`, true); w.Code != 503 {
			t.Fatal("mismatch accepted")
		}
		page, _ := h.inbox.Conversations(store.Filter{Platform: "weixin"})
		if len(page.Items) != 0 {
			t.Fatal("mismatch persisted")
		}
	}
	h := newEmailTestHandler(t)
	f := &fakeWechatReader{batch: wechatBatch()}
	bindWechat(h, f)
	if w := emailTestRequest(h, "POST", route, `{"peer_id":"wxid_peer"}`, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	f.batch.Messages = append(f.batch.Messages, wechatdb.Message{ExternalID: "new", Direction: "incoming", Body: "must rollback", OccurredAt: "2026-01-01T00:03:00Z"})
	f.batch.Messages[0].Body = "conflicting source edit"
	if w := emailTestRequest(h, "POST", route, `{"peer_id":"wxid_peer"}`, true); w.Code != 409 {
		t.Fatal("source edit must conflict", w.Code)
	}
	page, _ := h.inbox.Conversations(store.Filter{Platform: "weixin"})
	msgs, _ := h.inbox.Messages(page.Items[0].ID, "", 50)
	if len(msgs.Items) != 2 {
		t.Fatal("partial read committed")
	}
	for _, failure := range []error{wechatdb.ErrInvalidKey, wechatdb.ErrChanged, wechatdb.ErrNotFound, wechatdb.ErrUnsupportedSchema, errors.New("secret-key C:\\private\\account")} {
		f.err = failure
		w := emailTestRequest(h, "POST", route, `{"peer_id":"wxid_peer"}`, true)
		if w.Code != 503 || strings.Contains(w.Body.String(), "secret-key") || strings.Contains(w.Body.String(), "private") {
			t.Fatal("unredacted source error", w.Body.String())
		}
	}
}

func TestWechatReaderIsSerializedAndCanceledReadCannotCommit(t *testing.T) {
	h := newEmailTestHandler(t)
	f := &fakeWechatReader{batch: wechatBatch(), entered: make(chan struct{}), release: make(chan struct{})}
	bindWechat(h, f)
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest("POST", "http://127.0.0.1:17890/api/v1/connections/wechat-db/read", strings.NewReader(`{"peer_id":"wxid_peer"}`)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+emailTestToken)
	done := make(chan struct{})
	go func() { defer close(done); h.ServeHTTP(httptest.NewRecorder(), r) }()
	<-f.entered
	w := emailTestRequest(h, "GET", "/api/v1/connections/wechat-db/sessions", "", true)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "WECHAT_BUSY") || f.calls.Load() != 1 {
		t.Fatal("source gate bypass")
	}
	cancel()
	<-done
	page, _ := h.inbox.Conversations(store.Filter{Platform: "weixin"})
	if len(page.Items) != 0 {
		t.Fatal("canceled read committed")
	}
}
