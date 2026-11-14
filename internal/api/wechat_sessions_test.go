package api

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qilinbro/msg-manager/internal/connector/wechatdb"
	"github.com/qilinbro/msg-manager/internal/store"
)

type singlePassWechatTestReader struct {
	base        *fakeWechatReader
	singleCalls atomic.Int32
	legacyCalls atomic.Int32
	info        wechatdb.Info
	sessionFn   func(context.Context) error
	readFn      func(context.Context) (wechatdb.Batch, error)
}

func (f *singlePassWechatTestReader) Probe(context.Context) (wechatdb.Info, error) {
	f.legacyCalls.Add(1)
	return wechatdb.Info{}, errors.New("single-pass reader must not be probed separately")
}

func (f *singlePassWechatTestReader) Sessions(context.Context) ([]wechatdb.Session, error) {
	f.legacyCalls.Add(1)
	return nil, errors.New("single-pass reader must not be listed separately")
}

func (f *singlePassWechatTestReader) SessionsWithInfo(ctx context.Context) (wechatdb.Info, []wechatdb.Session, error) {
	f.singleCalls.Add(1)
	if f.sessionFn != nil {
		if err := f.sessionFn(ctx); err != nil {
			return wechatdb.Info{}, nil, err
		}
	}
	return f.info, []wechatdb.Session{{PeerID: "wxid_peer", Name: "合成联系人"}}, nil
}

func (f *singlePassWechatTestReader) Read(ctx context.Context, peer string) (wechatdb.Batch, error) {
	if f.readFn != nil {
		return f.readFn(ctx)
	}
	return f.base.Read(ctx, peer)
}

func bindSinglePassWechat(t *testing.T) (*handler, *singlePassWechatTestReader) {
	t.Helper()
	h := newEmailTestHandler(t)
	base := &fakeWechatReader{batch: wechatBatch()}
	bindWechat(h, base)
	f := &singlePassWechatTestReader{base: base, info: base.batch.Info}
	h.wechat.reader = f
	return h, f
}

func TestWechatSessionListingAndMonitorUseOneValidatedRead(t *testing.T) {
	h, f := bindSinglePassWechat(t)
	w := emailTestRequest(h, "GET", "/api/v1/connections/wechat-db/sessions", "", true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"peer_id":"wxid_peer"`) || f.singleCalls.Load() != 1 || f.legacyCalls.Load() != 0 {
		t.Fatal("session listing repeated the validated source read", w.Code)
	}
	if err := h.validateWechatMonitorPeers(context.Background(), []string{"wxid_peer"}); err != nil {
		t.Fatal("valid monitor selection rejected", err)
	}
	if f.singleCalls.Load() != 2 || f.legacyCalls.Load() != 0 {
		t.Fatal("monitor validation repeated the source read")
	}
	if err := h.validateWechatMonitorPeers(context.Background(), []string{"wxid_missing"}); !errors.Is(err, store.ErrInvalidInput) {
		t.Fatal("single-pass validation admitted an unlisted peer", err)
	}
}

func TestWechatSessionSinglePassStillChecksIdentityAndReadonly(t *testing.T) {
	for _, change := range []func(*wechatdb.Info){
		func(info *wechatdb.Info) { info.AccountID = "wxid_other" },
		func(info *wechatdb.Info) { info.ReadOnly = false },
	} {
		h, f := bindSinglePassWechat(t)
		change(&f.info)
		w := emailTestRequest(h, "GET", "/api/v1/connections/wechat-db/sessions", "", true)
		if w.Code != 503 || !strings.Contains(w.Body.String(), "WECHAT_UNSUPPORTED_SCHEMA") || strings.Contains(w.Body.String(), "合成联系人") {
			t.Fatal("single-pass identity failure exposed sessions", w.Code)
		}
		if err := h.validateWechatMonitorPeers(context.Background(), []string{"wxid_peer"}); !errors.Is(err, wechatdb.ErrUnsupportedSchema) {
			t.Fatal("monitor accepted mismatched single-pass identity", err)
		}
	}
	// An adapter with only the original interface keeps its early Probe checks.
	h := newEmailTestHandler(t)
	legacy := &fakeWechatReader{batch: wechatBatch()}
	bindWechat(h, legacy)
	if _, err := h.validatedWechatSessions(context.Background()); err != nil || legacy.calls.Load() != 2 {
		t.Fatal("legacy reader compatibility failed", err)
	}
	legacy.calls.Store(0)
	legacy.batch.Info.AccountID = "wxid_other"
	if _, err := h.validatedWechatSessions(context.Background()); !errors.Is(err, wechatdb.ErrUnsupportedSchema) || legacy.calls.Load() != 1 {
		t.Fatal("legacy mismatched probe proceeded to list sessions", err)
	}
}

type sourceDeadlineTestContext struct {
	context.Context
	expired atomic.Bool
}

func (c *sourceDeadlineTestContext) Err() error {
	if c.expired.Load() {
		return context.DeadlineExceeded
	}
	return nil
}

type legacyWechatDeadlineTestReader struct {
	base       *fakeWechatReader
	probeFn    func(context.Context) error
	sessionsFn func(context.Context) error
}

func (f *legacyWechatDeadlineTestReader) Probe(ctx context.Context) (wechatdb.Info, error) {
	if f.probeFn != nil {
		if err := f.probeFn(ctx); err != nil {
			return wechatdb.Info{}, err
		}
	}
	return f.base.Probe(ctx)
}

func (f *legacyWechatDeadlineTestReader) Sessions(ctx context.Context) ([]wechatdb.Session, error) {
	if f.sessionsFn != nil {
		if err := f.sessionsFn(ctx); err != nil {
			return nil, err
		}
	}
	return f.base.Sessions(ctx)
}

func (f *legacyWechatDeadlineTestReader) Read(ctx context.Context, peer string) (wechatdb.Batch, error) {
	return f.base.Read(ctx, peer)
}

func TestWechatContextDeadlineTakesPrecedenceOverCollapsedSourceError(t *testing.T) {
	for _, stage := range []string{"single-pass", "legacy-probe", "legacy-sessions", "messages"} {
		t.Run(stage, func(t *testing.T) {
			h, f := bindSinglePassWechat(t)
			ctx := &sourceDeadlineTestContext{Context: context.Background()}
			expire := func(context.Context) error {
				ctx.expired.Store(true)
				return wechatdb.ErrUnavailable
			}
			var err error
			switch stage {
			case "single-pass":
				f.sessionFn = expire
			case "legacy-probe", "legacy-sessions":
				legacy := &legacyWechatDeadlineTestReader{base: f.base}
				if stage == "legacy-probe" {
					legacy.probeFn = expire
				} else {
					legacy.sessionsFn = expire
				}
				h.wechat.reader = legacy
			case "messages":
				f.readFn = func(ctx context.Context) (wechatdb.Batch, error) {
					return wechatdb.Batch{}, expire(ctx)
				}
				_, err = h.readWechatPeer(ctx, "wxid_peer")
			}
			if stage != "messages" {
				_, err = h.validatedWechatSessions(ctx)
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("source iteration lost its context deadline", err)
			}
			if wechatMonitorErrorCode(err) != "WECHAT_TIMEOUT" {
				t.Fatal("background deadline was mislabeled")
			}
			page, listErr := h.inbox.Conversations(store.Filter{Platform: "weixin"})
			if listErr != nil || len(page.Items) != 0 {
				t.Fatal("expired metadata/message read persisted data", listErr)
			}
		})
	}
}

func TestWechatTimeoutResponseIsSpecificAndDoesNotExposeSource(t *testing.T) {
	h, f := bindSinglePassWechat(t)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	r := httptest.NewRequest("GET", "http://127.0.0.1:17890/api/v1/connections/wechat-db/sessions", nil).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+emailTestToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "WECHAT_TIMEOUT") || f.singleCalls.Load() != 0 {
		t.Fatal("expired listing did not report timeout without touching source", w.Code)
	}
	h.wechat.mu.RLock()
	state := h.wechat.status.State
	h.wechat.mu.RUnlock()
	if state != "unavailable" {
		t.Fatal("timeout introduced a different cached state")
	}
	w = httptest.NewRecorder()
	h.failWechat(w, fmt.Errorf("secret-database-path-and-key: %w", context.DeadlineExceeded), "synthetic-trace")
	if !strings.Contains(w.Body.String(), "WECHAT_TIMEOUT") || strings.Contains(w.Body.String(), "secret-database-path-and-key") {
		t.Fatal("wrapped source timeout was not safely classified")
	}
}
