package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qilinbro/msg-manager/internal/config"
	"github.com/qilinbro/msg-manager/internal/connector/wechatdb"
	"github.com/qilinbro/msg-manager/internal/store"
)

const monitorRoute = "/api/v1/connections/wechat-db/monitor"

type monitorTestReader struct {
	probes  atomic.Int32
	reads   atomic.Int32
	account string
	readFn  func(context.Context, string) (wechatdb.Batch, error)
}

func (f *monitorTestReader) Probe(context.Context) (wechatdb.Info, error) {
	f.probes.Add(1)
	return wechatdb.Info{AccountID: f.account, ReadOnly: true}, nil
}

func (f *monitorTestReader) Sessions(context.Context) ([]wechatdb.Session, error) {
	return []wechatdb.Session{{PeerID: "wxid_peer", Name: "合成甲"}, {PeerID: "wxid_second", Name: "合成乙"}}, nil
}

func (f *monitorTestReader) Read(ctx context.Context, peer string) (wechatdb.Batch, error) {
	f.reads.Add(1)
	if f.readFn != nil {
		return f.readFn(ctx, peer)
	}
	b := wechatBatch()
	b.Info.AccountID, b.PeerID = f.account, peer
	for i := range b.Messages {
		b.Messages[i].ExternalID += "-" + peer
	}
	return b, nil
}

func bindMonitorReader(h *handler, f *monitorTestReader) {
	if f.account == "" {
		f.account = "wxid_synthetic"
	}
	h.wechat.reader, h.wechat.accountID = f, f.account
	account := f.account
	h.wechat.status = WechatStatus{Configured: true, State: "ready", AccountID: &account, ReadOnly: true}
}

func monitorTestClock(c *wechatConnection) (chan struct{}, chan struct{}) {
	ticks := make(chan struct{})
	cycles := make(chan struct{}, 8)
	c.monitorWait = func(ctx context.Context, _ time.Duration) bool {
		cycles <- struct{}{}
		select {
		case <-ctx.Done():
			return false
		case <-ticks:
			return true
		}
	}
	return ticks, cycles
}

func awaitMonitorCycle(t *testing.T, cycles <-chan struct{}) {
	t.Helper()
	select {
	case <-cycles:
	case <-time.After(3 * time.Second):
		t.Fatal("background cycle did not complete")
	}
}

func monitorInput(enabled bool, peers ...string) string {
	if peers == nil {
		peers = []string{}
	}
	raw, _ := json.Marshal(store.WechatMonitorSettings{Enabled: enabled, PeerIDs: peers, IntervalSeconds: 30})
	return string(raw)
}

func getMonitorStatus(t *testing.T, h *handler) WechatMonitorStatus {
	t.Helper()
	w := emailTestRequest(h, "GET", "/api/v1/connections/wechat-db/status", "", true)
	var status WechatStatus
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &status) != nil {
		t.Fatal("status", w.Code, w.Body.String())
	}
	return status.Monitor
}

func TestWechatMonitorDefaultValidationAndScope(t *testing.T) {
	h := newEmailTestHandler(t)
	f := &monitorTestReader{}
	bindMonitorReader(h, f)
	status := getMonitorStatus(t, h)
	if status.Enabled || status.State != "disabled" || status.IntervalSeconds != 30 || status.PeerIDs == nil || f.reads.Load() != 0 || f.probes.Load() != 0 {
		t.Fatal("status started reading", status)
	}
	if w := emailTestRequest(h, "POST", monitorRoute, monitorInput(true, "wxid_peer"), false); w.Code != 401 {
		t.Fatal("unauthenticated", w.Code)
	}
	for _, body := range []string{`{}`, `null`, `{"enabled":true,"peer_ids":[],"interval_seconds":30}`, `{"enabled":false,"peer_ids":null,"interval_seconds":30}`, `{"enabled":true,"peer_ids":["wxid_peer","wxid_peer"],"interval_seconds":30}`, `{"enabled":true,"peer_ids":["wxid_peer"],"interval_seconds":9}`, `{"enabled":true,"peer_ids":["wxid_peer"],"interval_seconds":301}`, `{"enabled":true,"peer_ids":["wxid_peer"],"interval_seconds":30,"key":"secret"}`, `{"enabled":true,"enabled":false,"peer_ids":["wxid_peer"],"interval_seconds":30}`, `{"enabled":true,"peer_ids":["room@chatroom"],"interval_seconds":30}`} {
		if w := emailTestRequest(h, "POST", monitorRoute, body, true); w.Code != 400 {
			t.Fatal("invalid monitor accepted", body, w.Code)
		}
	}
	if f.probes.Load() != 0 || f.reads.Load() != 0 {
		t.Fatal("invalid settings touched source")
	}
	if w := emailTestRequest(h, "POST", monitorRoute+"?directory=private", monitorInput(false), true); w.Code != 400 {
		t.Fatal("query accepted")
	}
	if w := emailTestRequest(h, "POST", monitorRoute, monitorInput(true, "wxid_absent"), true); w.Code != 400 {
		t.Fatal("unknown peer accepted", w.Code)
	}
	if f.reads.Load() != 0 {
		t.Fatal("rejected subscription started worker")
	}
	f.account = "wxid_other"
	if w := emailTestRequest(h, "POST", monitorRoute, monitorInput(true, "wxid_peer"), true); w.Code != 503 {
		t.Fatal("wrong account accepted", w.Code)
	}
	preflight := httptest.NewRequest("OPTIONS", "http://127.0.0.1:17890"+monitorRoute, nil)
	preflight.Header.Set("Origin", "http://127.0.0.1:1420")
	preflight.Header.Set("Access-Control-Request-Method", "POST")
	preflight.Header.Set("Access-Control-Request-Headers", "authorization, content-type")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, preflight)
	if w.Code != 204 {
		t.Fatal("monitor CORS", w.Code)
	}
	preflight.Header.Set("Origin", "https://untrusted.example")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, preflight)
	if w.Code != 403 {
		t.Fatal("untrusted origin", w.Code)
	}
}

func TestWechatMonitorSurvivesRequestAndDeduplicates(t *testing.T) {
	h := newEmailTestHandler(t)
	f := &monitorTestReader{}
	bindMonitorReader(h, f)
	ticks, cycles := monitorTestClock(h.wechat)
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest("POST", "http://127.0.0.1:17890"+monitorRoute, strings.NewReader(monitorInput(true, "wxid_peer"))).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+emailTestToken)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	cancel()
	if w.Code != 200 {
		t.Fatal("enable", w.Code, w.Body.String())
	}
	awaitMonitorCycle(t, cycles)
	status := getMonitorStatus(t, h)
	if !status.Enabled || status.State != "waiting" || status.LastResult == nil || status.LastResult.Inserted != 2 || !status.LastResult.Limited || status.SendingEnabled || !status.ReadOnly {
		t.Fatal("first cycle", status)
	}
	if f.reads.Load() != 1 {
		t.Fatal("first background read", f.reads.Load())
	}
	ticks <- struct{}{}
	awaitMonitorCycle(t, cycles)
	status = getMonitorStatus(t, h)
	if status.LastResult.Inserted != 0 || status.LastResult.Duplicates != 2 || f.reads.Load() != 2 {
		t.Fatal("dedup cycle", status)
	}
	w = emailTestRequest(h, "POST", monitorRoute, monitorInput(false, "wxid_peer"), true)
	if w.Code != 200 {
		t.Fatal("pause", w.Code, w.Body.String())
	}
	status = getMonitorStatus(t, h)
	if status.Enabled || status.State != "disabled" {
		t.Fatal("pause state", status)
	}
	stored, err := h.inbox.LoadWechatMonitor(context.Background(), "wxid_synthetic")
	if err != nil || stored.Enabled {
		t.Fatal("pause not persisted", stored, err)
	}
	if w := emailTestRequest(h, "POST", "/api/v1/send", `{}`, true); w.Code != 501 {
		t.Fatal("monitor enabled sending")
	}
}

func TestWechatMonitorPauseCancelsBeforeImport(t *testing.T) {
	h := newEmailTestHandler(t)
	entered, finished := make(chan struct{}), make(chan struct{})
	f := &monitorTestReader{readFn: func(ctx context.Context, _ string) (wechatdb.Batch, error) {
		close(entered)
		<-ctx.Done()
		close(finished)
		return wechatBatch(), nil
	}}
	bindMonitorReader(h, f)
	if w := emailTestRequest(h, "POST", monitorRoute, monitorInput(true, "wxid_peer"), true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("worker not started")
	}
	if w := emailTestRequest(h, "POST", monitorRoute, monitorInput(false, "wxid_peer"), true); w.Code != 200 {
		t.Fatal("pause blocked", w.Code, w.Body.String())
	}
	select {
	case <-finished:
	default:
		t.Fatal("pause returned before canceled read exited")
	}
	page, err := h.inbox.Conversations(store.Filter{Platform: "weixin"})
	if err != nil || len(page.Items) != 0 {
		t.Fatal("canceled batch persisted", err)
	}
	if f.reads.Load() != 1 {
		t.Fatal("pause read more messages")
	}
}

func TestWechatMonitorGateAndPauseWhileManualBusy(t *testing.T) {
	h := newEmailTestHandler(t)
	f := &monitorTestReader{}
	bindMonitorReader(h, f)
	ticks, cycles := monitorTestClock(h.wechat)
	if w := emailTestRequest(h, "POST", monitorRoute, monitorInput(true, "wxid_peer"), true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	awaitMonitorCycle(t, cycles)
	h.wechat.gate <- struct{}{} // A foreground operation owns the gate.
	ticks <- struct{}{}
	if w := emailTestRequest(h, "POST", monitorRoute, monitorInput(false, "wxid_peer"), true); w.Code != 200 {
		t.Fatal("pause waited for manual read", w.Code)
	}
	if len(h.wechat.gate) != 1 || f.reads.Load() != 1 {
		t.Fatal("background bypassed or stole manual gate")
	}
	<-h.wechat.gate
}

func TestWechatMonitorPartialSuccessAndErrorRedaction(t *testing.T) {
	h := newEmailTestHandler(t)
	f := &monitorTestReader{readFn: func(_ context.Context, peer string) (wechatdb.Batch, error) {
		if peer == "wxid_second" {
			return wechatdb.Batch{}, errors.New("private path and key must not appear")
		}
		return wechatBatch(), nil
	}}
	bindMonitorReader(h, f)
	_, cycles := monitorTestClock(h.wechat)
	if w := emailTestRequest(h, "POST", monitorRoute, monitorInput(true, "wxid_peer", "wxid_second"), true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	awaitMonitorCycle(t, cycles)
	status := getMonitorStatus(t, h)
	if status.State != "error" || status.LastResult == nil || status.LastResult.Inserted != 2 || status.LastErrorCode == nil || *status.LastErrorCode != "WECHAT_UNAVAILABLE" {
		t.Fatal("partial result lost", status)
	}
	w := emailTestRequest(h, "GET", "/api/v1/connections/wechat-db/status", "", true)
	if strings.Contains(w.Body.String(), "private path") || strings.Contains(w.Body.String(), "key must") {
		t.Fatal("source error leaked")
	}
}

func TestWechatMonitorCloseAndRestoreAreAccountScoped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "monitor.db")
	open := func(account string) *handler {
		actual, err := NewHandler(config.Config{Addr: config.DefaultAddr, APIToken: emailTestToken, DatabasePath: path, DatabaseKey: strings.Repeat("ab", 32)})
		if err != nil {
			t.Fatal(err)
		}
		h := actual.(*handler)
		bindMonitorReader(h, &monitorTestReader{account: account})
		t.Cleanup(func() { _ = h.Close() })
		return h
	}
	h := open("wxid_synthetic")
	_, cycles := monitorTestClock(h.wechat)
	if w := emailTestRequest(h, "POST", monitorRoute, monitorInput(true, "wxid_peer"), true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	awaitMonitorCycle(t, cycles)
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if h.wechat.cancelMonitor != nil || h.wechat.monitorDone != nil {
		t.Fatal("Close did not join worker")
	}
	h2 := open("wxid_other")
	_, otherCycles := monitorTestClock(h2.wechat)
	if err := h2.restoreWechatMonitor(); err != nil {
		t.Fatal(err)
	}
	if getMonitorStatus(t, h2).Enabled || h2.wechat.reader.(*monitorTestReader).reads.Load() != 0 || len(otherCycles) != 0 {
		t.Fatal("another account inherited subscription")
	}
	_ = h2.Close()
	h3 := open("wxid_synthetic")
	_, cycles3 := monitorTestClock(h3.wechat)
	if err := h3.restoreWechatMonitor(); err != nil {
		t.Fatal(err)
	}
	awaitMonitorCycle(t, cycles3)
	if !getMonitorStatus(t, h3).Enabled || h3.wechat.reader.(*monitorTestReader).reads.Load() != 1 {
		t.Fatal("durable subscription not resumed")
	}
}

func TestWechatMonitorCanceledUpdateLeavesWorkerAlone(t *testing.T) {
	h := newEmailTestHandler(t)
	f := &monitorTestReader{}
	bindMonitorReader(h, f)
	_, cycles := monitorTestClock(h.wechat)
	if w := emailTestRequest(h, "POST", monitorRoute, monitorInput(true, "wxid_peer"), true); w.Code != 200 {
		t.Fatal(w.Code)
	}
	awaitMonitorCycle(t, cycles)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("POST", "http://127.0.0.1:17890"+monitorRoute, strings.NewReader(monitorInput(false))).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+emailTestToken)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 503 || !getMonitorStatus(t, h).Enabled {
		t.Fatal("canceled update changed subscription", w.Code)
	}
}

func TestWechatMonitorBatchAccountMismatchAndPersistenceFailure(t *testing.T) {
	t.Run("batch account is checked again", func(t *testing.T) {
		h := newEmailTestHandler(t)
		f := &monitorTestReader{readFn: func(context.Context, string) (wechatdb.Batch, error) {
			batch := wechatBatch()
			batch.Info.AccountID = "wxid_another_account"
			return batch, nil
		}}
		bindMonitorReader(h, f)
		_, cycles := monitorTestClock(h.wechat)
		if w := emailTestRequest(h, "POST", monitorRoute, monitorInput(true, "wxid_peer"), true); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		awaitMonitorCycle(t, cycles)
		status := getMonitorStatus(t, h)
		if status.State != "error" || status.LastErrorCode == nil || *status.LastErrorCode != "WECHAT_UNSUPPORTED_SCHEMA" || status.LastResult.Inserted != 0 {
			t.Fatal("mismatched batch accepted", status)
		}
		page, err := h.inbox.Conversations(store.Filter{Platform: "weixin"})
		if err != nil || len(page.Items) != 0 {
			t.Fatal("wrong account persisted", err)
		}
	})
	t.Run("failed persistence never enables worker", func(t *testing.T) {
		h := newEmailTestHandler(t)
		f := &monitorTestReader{}
		bindMonitorReader(h, f)
		_ = h.inbox.Close()
		w := emailTestRequest(h, "POST", monitorRoute, monitorInput(true, "wxid_peer"), true)
		if w.Code != 503 || getMonitorStatus(t, h).Enabled || f.reads.Load() != 0 {
			t.Fatal("failed save enabled monitor", w.Code, w.Body.String())
		}
	})
}
