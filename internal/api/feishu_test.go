package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qilinbro/msg-manager/internal/agent"
	"github.com/qilinbro/msg-manager/internal/connector/feishu"
	"github.com/qilinbro/msg-manager/internal/store"
)

type feishuTestModel struct {
	calls   atomic.Int32
	chats   atomic.Int32
	entered chan agent.Input
	release chan struct{}
	failure error
	chat    func(context.Context, int, agent.ToolInput) (agent.ToolResult, error)
}

func (m *feishuTestModel) Analyze(ctx context.Context, in agent.Input) (agent.Result, error) {
	m.calls.Add(1)
	if m.entered != nil {
		m.entered <- in
	}
	if m.release != nil {
		select {
		case <-m.release:
		case <-ctx.Done():
			return agent.Result{}, ctx.Err()
		}
	}
	if m.failure != nil {
		return agent.Result{}, m.failure
	}
	ids := []string{}
	if len(in.Messages) > 0 {
		ids = append(ids, in.Messages[0].ID)
	}
	return agent.Result{Output: agent.Output{Intent: "information_request", Summary: "合成摘要：确认合同资料。", Draft: "MODEL_DRAFT_MUST_NOT_BECOME_TODO", RiskFlags: []string{}, EvidenceIDs: ids, MemoryCandidates: []agent.MemoryCandidate{}}}, nil
}
func (m *feishuTestModel) ChatTools(ctx context.Context, in agent.ToolInput) (agent.ToolResult, error) {
	index := int(m.chats.Add(1))
	if m.chat != nil {
		return m.chat(ctx, index, in)
	}
	return assistantFixtureAnswer("合成只读结果"), nil
}

type feishuTestSender struct {
	calls   atomic.Int32
	entered chan feishu.SendInput
	release chan struct{}
}

func (s *feishuTestSender) SendText(ctx context.Context, c feishu.Credentials, in feishu.SendInput) (feishu.Receipt, error) {
	s.calls.Add(1)
	if err := feishu.ValidateInput(c, in); err != nil {
		return feishu.Receipt{}, err
	}
	if s.entered != nil {
		s.entered <- in
	}
	if s.release != nil {
		select {
		case <-s.release:
		case <-ctx.Done():
			return feishu.Receipt{}, &feishu.Error{Code: feishu.CodeSendUnknown, Unknown: true}
		}
	}
	return feishu.Receipt{MessageID: "om_synthetic_receipt"}, nil
}
func feishuJSON(value any) string { raw, _ := json.Marshal(value); return string(raw) }
func newFeishuTestFixture(t *testing.T, withMessage bool) (*handler, *feishuTestModel, *feishuTestSender, store.FeishuBinding, string) {
	t.Helper()
	h, _, first, _ := newAPIProfileFixture(t)
	model := &feishuTestModel{}
	sender := &feishuTestSender{}
	h.ai.client = model
	h.ai.dailyLimit = 100
	now := time.Date(2026, 11, 17, 12, 5, 0, 0, time.UTC)
	h.feishu.now = func() time.Time { return now }
	h.feishu.sender = sender
	w := emailTestRequest(h, "PATCH", feishuPrefix+"/config", feishuJSON(map[string]any{"expected_revision": 1, "app_id": "cli_synthetic_bot", "app_secret": "synthetic-feishu-secret-no-platform-io", "retain_secret": false, "recipient_open_id": "ou_synthetic_own_private_target", "confirmed": true}), true)
	if w.Code != 200 {
		t.Fatalf("config: %d %s", w.Code, w.Body.String())
	}
	cfg, err := h.inbox.LoadFeishuConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	binding := store.FeishuBinding{ModelConfigID: apiProfileConfig, ModelConfigRevision: 0, ConfigRevision: cfg.Revision, TargetBindingID: cfg.TargetBindingID()}
	if withMessage {
		_, err = h.inbox.Import(store.ImportRequest{Platform: "qq", AccountID: "synthetic-profile-account", AccountName: "合成测试账号", Messages: []store.ImportMessage{{ExternalID: "feishu-window-message", PeerID: "peer-one", PeerName: "合成联系人", Body: "请今天确认合成合同 <at user_id=\"all\">@all</at>", Direction: "incoming", OccurredAt: "2026-11-17T11:00:00Z"}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	return h, model, sender, binding, first
}
func feishuPreviewBody(b store.FeishuBinding) string {
	return feishuJSON(struct {
		Confirmed bool `json:"confirmed"`
		store.FeishuBinding
	}{true, b})
}
func feishuSendBody(r store.FeishuReport) string {
	return feishuJSON(struct {
		Confirmed   bool   `json:"confirmed"`
		ContentHash string `json:"content_hash"`
		store.FeishuBinding
	}{true, r.ContentHash, r.FeishuBinding})
}
func feishuReportResponse(t *testing.T, w *httptest.ResponseRecorder) store.FeishuReport {
	t.Helper()
	var envelope struct {
		Report *store.FeishuReport `json:"report"`
	}
	if w.Code != 200 && w.Code != 202 || json.Unmarshal(w.Body.Bytes(), &envelope) != nil || envelope.Report == nil {
		t.Fatalf("report: %d %s", w.Code, w.Body.String())
	}
	return *envelope.Report
}
func waitFeishuReport(t *testing.T, h *handler, id string) store.FeishuReport {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		r, err := h.inbox.FeishuReportByID(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if r != nil && r.State != "running" {
			return *r
		}
		select {
		case <-deadline.C:
			t.Fatal("report worker did not finish")
		case <-tick.C:
		}
	}
}
func waitFeishuDelivery(t *testing.T, h *handler, id string) store.FeishuDelivery {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		d, err := h.inbox.FeishuDeliveryByID(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if d != nil && d.State != "running" {
			return *d
		}
		select {
		case <-deadline.C:
			t.Fatal("delivery worker did not finish")
		case <-tick.C:
		}
	}
}
func receiveFeishuInput(t *testing.T, ch <-chan agent.Input) agent.Input {
	t.Helper()
	select {
	case in := <-ch:
		return in
	case <-time.After(3 * time.Second):
		t.Fatal("model was not called")
		return agent.Input{}
	}
}

func TestFeishuAsyncPreviewSendOneShotAndPrivateHistory(t *testing.T) {
	h, model, sender, binding, _ := newFeishuTestFixture(t, true)
	model.entered = make(chan agent.Input, 4)
	model.release = make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(model.release)
		}
	}()
	w := emailTestRequest(h, "POST", feishuPrefix+"/preview", feishuPreviewBody(binding), true)
	accepted := feishuReportResponse(t, w)
	if w.Code != 202 || accepted.State != "running" || accepted.Text != "" || accepted.Summary != "" || len(accepted.Todos) != 0 || len(accepted.Excerpts) != 0 {
		t.Fatal("accepted is not completion", w.Code, accepted.State)
	}
	in := receiveFeishuInput(t, model.entered)
	if len(in.Messages) != 1 || strings.Contains(in.Messages[0].Body, "<at") || strings.Contains(in.Messages[0].Body, "@all") {
		t.Fatal("unbounded or active markup input")
	}
	close(model.release)
	released = true
	report := waitFeishuReport(t, h, accepted.ID)
	if report.State != "succeeded" || report.GeneratedAt == nil || len(report.Todos) != 1 || !report.Todos[0].NeedsReview || len(report.SourceRefs) != 1 || strings.Contains(feishuJSON(report), "MODEL_DRAFT_MUST_NOT_BECOME_TODO") {
		t.Fatalf("not a valid immutable report: %+v", report)
	}
	if got := emailTestRequest(h, "GET", feishuPrefix+"/reports/"+report.ID, "", true); got.Code != 200 {
		t.Fatalf("report ID route: %d %s", got.Code, got.Body.String())
	}
	again := emailTestRequest(h, "POST", feishuPrefix+"/preview", feishuPreviewBody(binding), true)
	if again.Code != 200 || model.calls.Load() != 1 {
		t.Fatal("preview regenerated existing window", again.Code)
	}
	sender.entered = make(chan feishu.SendInput, 2)
	sender.release = make(chan struct{})
	sentReleased := false
	defer func() {
		if !sentReleased {
			close(sender.release)
		}
	}()
	sent := emailTestRequest(h, "POST", feishuPrefix+"/reports/"+report.ID+"/send", feishuSendBody(report), true)
	var deliveryEnvelope struct {
		Delivery store.FeishuDelivery `json:"delivery"`
	}
	_ = json.Unmarshal(sent.Body.Bytes(), &deliveryEnvelope)
	if sent.Code != 202 || deliveryEnvelope.Delivery.State != "running" {
		t.Fatalf("send acceptance: %d %s", sent.Code, sent.Body.String())
	}
	select {
	case input := <-sender.entered:
		if input.Text != report.Text || input.UUID != store.FeishuDeliveryID(report.WindowEnd) {
			t.Fatal("send did not freeze exact report")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("sender not called")
	}
	duplicate := emailTestRequest(h, "POST", feishuPrefix+"/reports/"+report.ID+"/send", feishuSendBody(report), true)
	if duplicate.Code != 200 || sender.calls.Load() != 1 {
		t.Fatal("duplicate send performed IO", duplicate.Code)
	}
	close(sender.release)
	sentReleased = true
	delivery := waitFeishuDelivery(t, h, deliveryEnvelope.Delivery.ID)
	if delivery.State != "succeeded" || delivery.CompletedAt == nil || delivery.MessageID == "" {
		t.Fatal("receipt missing", delivery.State)
	}
	quota, err := h.inbox.CountAICallsContext(context.Background(), "2026-11-17")
	if err != nil || quota != 1 {
		t.Fatal("per-call shared quota", quota, err)
	}
	saved := emailTestRequest(h, "PATCH", feishuPrefix+"/config", feishuJSON(map[string]any{"expected_revision": binding.ConfigRevision, "app_id": "cli_synthetic_bot", "app_secret": "", "retain_secret": true, "recipient_open_id": "ou_synthetic_own_private_target", "confirmed": true}), true)
	if saved.Code != 200 {
		t.Fatalf("same-value save: %d %s", saved.Code, saved.Body.String())
	}
	stale := emailTestRequest(h, "GET", feishuPrefix+"/reports/"+report.ID, "", true)
	if stale.Code != 409 || strings.Contains(stale.Body.String(), "合成合同") {
		t.Fatal("stale body readable after config save", stale.Code)
	}
	receipt := emailTestRequest(h, "GET", feishuPrefix+"/deliveries/"+delivery.ID, "", true)
	if receipt.Code != 200 || strings.Contains(receipt.Body.String(), "合成合同") || strings.Contains(receipt.Body.String(), "ou_synthetic_own_private_target") {
		t.Fatal("minimal delivery metadata unavailable/private body leaked", receipt.Code)
	}
}
