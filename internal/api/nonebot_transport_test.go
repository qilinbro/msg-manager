package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qilinbro/msg-manager/internal/config"
	"github.com/qilinbro/msg-manager/internal/connector/feishu"
	"github.com/qilinbro/msg-manager/internal/connector/nonebotbridge"
	"github.com/qilinbro/msg-manager/internal/store"
)

// This test seam never starts Python, an HTTP client, a real model or a platform.
// It exercises the real Go NoneBot sender, HTTP authorization and SQLCipher ledger.
type noneBotAPICaller struct {
	configured     atomic.Bool
	calls          atomic.Int32
	badPayload     atomic.Bool
	failure        *nonebotbridge.Error
	missingReceipt bool
	wantText       string
	wantUUID       string
}

func (c *noneBotAPICaller) Configured() bool { return c.configured.Load() }
func (c *noneBotAPICaller) Call(_ context.Context, action string, payload any, result any) error {
	c.calls.Add(1)
	raw, err := json.Marshal(payload)
	var fields map[string]string
	if err != nil || json.Unmarshal(raw, &fields) != nil || action != "feishu.send" || len(fields) != 5 || fields["app_id"] != "cli_synthetic_bot" || fields["app_secret"] != "synthetic-feishu-secret-no-platform-io" || fields["recipient_open_id"] != "ou_synthetic_own_private_target" || fields["text"] != c.wantText || fields["uuid"] != c.wantUUID {
		c.badPayload.Store(true)
	}
	if c.failure != nil {
		return c.failure
	}
	if c.missingReceipt {
		return nil
	}
	return json.Unmarshal([]byte(`{"message_id":"om_synthetic_nonebot_receipt"}`), result)
}

func TestFeishuNoneBotIsDefaultAndLegacyRequiresExplicitConfiguration(t *testing.T) {
	for _, transport := range []string{"", "nonebot", "legacy-go"} {
		t.Run("transport="+transport, func(t *testing.T) {
			service, err := NewHandler(config.Config{Addr: config.DefaultAddr, APIToken: strings.Repeat("t", 32), FeishuTransport: transport})
			if err != nil {
				t.Fatal(err)
			}
			h := service.(*handler)
			defer h.Close()
			_, noneBot := h.feishu.sender.(*feishu.NoneBotClient)
			if transport == "legacy-go" {
				if noneBot || h.feishu.transport != "legacy-go" {
					t.Fatal("explicit legacy transport not selected")
				}
			} else if !noneBot || h.feishu.transport != "nonebot" || h.feishu.available() {
				t.Fatal("missing runtime silently selected a legacy fallback or claimed availability")
			}
		})
	}
}

func noneBotAPIReport(t *testing.T, h *handler, binding store.FeishuBinding) store.FeishuReport {
	t.Helper()
	accepted := feishuReportResponse(t, emailTestRequest(h, http.MethodPost, feishuPrefix+"/preview", feishuPreviewBody(binding), true))
	report := waitFeishuReport(t, h, accepted.ID)
	if report.State != "succeeded" || report.Delivery != nil {
		t.Fatal("synthetic preview did not produce an unsent frozen report")
	}
	return report
}

func TestFeishuMissingNoneBotRuntimeDoesNotConsumeClaimAndReceiptsRemainReadable(t *testing.T) {
	h, model, _, binding, _ := newFeishuTestFixture(t, true)
	caller := &noneBotAPICaller{}
	h.feishu.sender = feishu.NewNoneBotClient(caller)
	report := noneBotAPIReport(t, h, binding)
	caller.wantText, caller.wantUUID = report.Text, store.FeishuDeliveryID(report.WindowEnd)
	status := emailTestRequest(h, http.MethodGet, feishuPrefix+"/status", "", true)
	var state feishuStatus
	if status.Code != 200 || json.Unmarshal(status.Body.Bytes(), &state) != nil || state.Transport != "nonebot" || state.RuntimeConfigured || !state.Configured || state.Capabilities.AutomaticRetry {
		t.Fatal("status did not distinguish saved credentials from absent runtime")
	}
	for range 2 {
		denied := emailTestRequest(h, http.MethodPost, feishuPrefix+"/reports/"+report.ID+"/send", feishuSendBody(report), true)
		if denied.Code != 503 || !strings.Contains(denied.Body.String(), feishu.CodeAuthUnavailable) {
			t.Fatal("missing runtime not rejected at preflight", denied.Code)
		}
	}
	stored, err := h.inbox.FeishuReportByID(context.Background(), report.ID)
	if err != nil || stored == nil || stored.Delivery != nil || caller.calls.Load() != 0 || model.calls.Load() != 1 {
		t.Fatal("unavailable runtime consumed durable claim or performed IO")
	}
	caller.configured.Store(true)
	accepted := emailTestRequest(h, http.MethodPost, feishuPrefix+"/reports/"+report.ID+"/send", feishuSendBody(report), true)
	if accepted.Code != 202 {
		t.Fatal("a preflight refusal burned the one-shot claim", accepted.Code)
	}
	delivery := waitFeishuDelivery(t, h, store.FeishuDeliveryID(report.WindowEnd))
	if delivery.State != "succeeded" || caller.calls.Load() != 1 || caller.badPayload.Load() {
		t.Fatal("configured runtime did not receive exactly the frozen bounded payload")
	}
	caller.configured.Store(false)
	for range 3 {
		replay := emailTestRequest(h, http.MethodPost, feishuPrefix+"/reports/"+report.ID+"/send", feishuSendBody(report), true)
		read := emailTestRequest(h, http.MethodGet, feishuPrefix+"/deliveries/"+delivery.ID, "", true)
		if replay.Code != 200 || read.Code != 200 || caller.calls.Load() != 1 {
			t.Fatal("receipt read or duplicate request started another send or required a runtime")
		}
	}
}

func TestFeishuNoneBotLedgerKeepsUnknownAndRejectionsOneShot(t *testing.T) {
	for _, tc := range []struct {
		name           string
		failure        *nonebotbridge.Error
		missingReceipt bool
		wantState      string
		wantCode       string
		revoked        bool
	}{
		{"lost reply", &nonebotbridge.Error{Kind: nonebotbridge.Unavailable, SendState: "unknown"}, false, "unknown", feishu.CodeSendUnknown, false},
		{"invalid worker reply", &nonebotbridge.Error{Kind: nonebotbridge.ProtocolError, SendState: "unknown"}, false, "unknown", feishu.CodeSendUnknown, false},
		{"missing receipt", nil, true, "unknown", feishu.CodeSendUnknown, false},
		{"permission 21001 before send", &nonebotbridge.Error{Kind: nonebotbridge.PermissionDenied, PlatformCode: 21001, SendState: "not_sent"}, false, "failed", feishu.CodePermissionDenied, true},
		{"rate limited before send", &nonebotbridge.Error{Kind: nonebotbridge.RateLimited, RetryAfter: 30 * time.Second, SendState: "not_sent"}, false, "failed", feishu.CodeRateLimited, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, _, binding, _ := newFeishuTestFixture(t, true)
			report := noneBotAPIReport(t, h, binding)
			caller := &noneBotAPICaller{failure: tc.failure, missingReceipt: tc.missingReceipt, wantText: report.Text, wantUUID: store.FeishuDeliveryID(report.WindowEnd)}
			caller.configured.Store(true)
			h.feishu.sender = feishu.NewNoneBotClient(caller)
			var schedule store.FeishuSchedule
			if tc.revoked {
				current, err := h.inbox.LoadFeishuSchedule(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal(struct {
					Enabled          bool  `json:"enabled"`
					ExpectedRevision int64 `json:"expected_revision"`
					Confirmed        bool  `json:"confirmed"`
					store.FeishuBinding
				}{true, current.Revision, true, binding})
				response := emailTestRequest(h, http.MethodPatch, feishuPrefix+"/schedule", string(raw), true)
				if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &schedule) != nil || !schedule.Enabled {
					t.Fatal("synthetic schedule not authorized")
				}
			}
			accepted := emailTestRequest(h, http.MethodPost, feishuPrefix+"/reports/"+report.ID+"/send", feishuSendBody(report), true)
			if accepted.Code != 202 {
				t.Fatal("send not accepted", accepted.Code)
			}
			delivery := waitFeishuDelivery(t, h, store.FeishuDeliveryID(report.WindowEnd))
			if delivery.State != tc.wantState || delivery.ErrorCode != tc.wantCode || delivery.MessageID != "" || caller.calls.Load() != 1 || caller.badPayload.Load() {
				t.Fatal("bridge failure was misclassified or replayed", delivery.State, delivery.ErrorCode)
			}
			for range 5 {
				replay := emailTestRequest(h, http.MethodPost, feishuPrefix+"/reports/"+report.ID+"/send", feishuSendBody(report), true)
				if replay.Code != 200 || caller.calls.Load() != 1 {
					t.Fatal("unknown/rejection was retried")
				}
			}
			if tc.revoked {
				// Join the accepted operation as revocation happens after receipt persistence.
				h.feishu.mu.Lock()
				done := len(h.feishu.operations) == 0
				h.feishu.mu.Unlock()
				if !done {
					h.feishu.wg.Wait()
				}
				current, err := h.inbox.LoadFeishuSchedule(context.Background())
				if err != nil || current.Enabled || current.Revision <= schedule.Revision {
					t.Fatal("21001 did not revoke the same frozen binding")
				}
			}
		})
	}
}
