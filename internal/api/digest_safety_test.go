package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qilinbro/msg-manager/internal/agent"
	"github.com/qilinbro/msg-manager/internal/store"
)

type digestStubAnalyzer struct {
	calls   atomic.Int32
	err     error
	inspect func(agent.Input)
}

func (stub *digestStubAnalyzer) Analyze(_ context.Context, input agent.Input) (agent.Result, error) {
	stub.calls.Add(1)
	if stub.inspect != nil {
		stub.inspect(input)
	}
	if stub.err != nil {
		return agent.Result{}, stub.err
	}
	return agent.Result{Output: agent.Output{Intent: "information_request", Summary: "合成摘要：请人工核对", Draft: "合成草稿不发送", RiskFlags: []string{}, EvidenceIDs: []string{input.Messages[0].ID}, MemoryCandidates: []agent.MemoryCandidate{}}}, nil
}

func newDigestTestHandler(t *testing.T, stub *digestStubAnalyzer) *handler {
	h := newEmailTestHandler(t)
	h.ai = &aiConnection{client: stub, gate: make(chan struct{}, 1), model: "synthetic-digest-model", provider: "synthetic", endpointHost: "synthetic.invalid", configID: strings.Repeat("d1", 32), external: true, dailyLimit: 10}
	h.modelConfig = store.ModelConfig{BaseURL: "https://synthetic.invalid", Model: h.ai.model, APIKey: "synthetic-only-never-sent", DailyLimit: 10, Revision: 7}
	return h
}

func digestTestJSON(value any) string { raw, _ := json.Marshal(value); return string(raw) }
func digestConsentBody(h *handler) string {
	return digestTestJSON(map[string]any{"confirmed": true, "model_config_id": h.ai.configID, "model_config_revision": h.modelConfig.Revision})
}
func digestScheduleBody(h *handler, enabled bool, revision int64) string {
	return digestTestJSON(map[string]any{"enabled": enabled, "local_time": "09:30", "timezone": "Asia/Shanghai", "expected_revision": revision, "confirmed": true, "model_config_id": h.ai.configID, "model_config_revision": h.modelConfig.Revision})
}
func seedDigestMessage(t *testing.T, h *handler, peer, body string) string {
	t.Helper()
	result, err := h.inbox.Import(store.ImportRequest{Platform: "qq", AccountID: "synthetic-digest-account", Messages: []store.ImportMessage{{ExternalID: peer + "-message", PeerID: peer, PeerName: "合成联系人-" + peer, Direction: "incoming", Body: body, OccurredAt: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)}}})
	if err != nil || len(result.ConversationIDs) != 1 {
		t.Fatal("seed digest", result, err)
	}
	return result.ConversationIDs[0]
}

func TestDigestExplicitBoundConsentBeforeAnyProviderOrQuota(t *testing.T) {
	stub := &digestStubAnalyzer{}
	h := newDigestTestHandler(t, stub)
	seedDigestMessage(t, h, "consent", "请确认合成信息？")
	valid := digestConsentBody(h)
	for _, body := range []string{
		`{}`, `null`, `[]`, valid + ` {}`, strings.Replace(valid, `"confirmed":true`, `"confirmed":false`, 1), strings.Replace(valid, `"confirmed":true`, `"confirmed":null`, 1), strings.Replace(valid, `"confirmed":true`, `"Confirmed":true`, 1), strings.Replace(valid, `"confirmed":true`, `"confirmed":true,"confirmed":false`, 1), strings.Replace(valid, `"model_config_revision":7`, `"model_config_revision":null`, 1), strings.Replace(valid, `"model_config_revision":7`, `"model_config_revision":"7"`, 1), strings.Replace(valid, `"model_config_id"`, `"MODEL_CONFIG_ID"`, 1), strings.Replace(valid, `}`, `,"api_key":"synthetic-forbidden"}`, 1), strings.Replace(valid, `}`, `,"padding":"`+strings.Repeat("x", 5000)+`"}`, 1),
	} {
		w := emailTestRequest(h, http.MethodPost, "/api/v1/digest/run", body, true)
		if w.Code != http.StatusBadRequest {
			t.Fatal("ambiguous or unconfirmed body accepted", w.Code, w.Body.String())
		}
	}
	for _, body := range []string{strings.Replace(valid, h.ai.configID, strings.Repeat("a2", 32), 1), strings.Replace(valid, `"model_config_revision":7`, `"model_config_revision":6`, 1)} {
		w := emailTestRequest(h, http.MethodPost, "/api/v1/digest/run", body, true)
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "DIGEST_MODEL_CHANGED") {
			t.Fatal("stale destination accepted", w.Code, w.Body.String())
		}
	}
	if w := emailTestRequest(h, http.MethodPost, "/api/v1/digest/run", valid, false); w.Code != 401 {
		t.Fatal("unauthenticated consent", w.Code)
	}
	used, err := h.inbox.CountAICalls(aiDay())
	report, reportErr := h.inbox.LatestDigestReport(context.Background())
	if stub.calls.Load() != 0 || used != 0 || err != nil || reportErr != nil || report != nil {
		t.Fatal("invalid consent performed work", stub.calls.Load(), used, err, report, reportErr)
	}
}

func TestDigestPersistsCanonicalReportAndNeverReplaysDailyModelWork(t *testing.T) {
	stub := &digestStubAnalyzer{}
	h := newDigestTestHandler(t, stub)
	seedDigestMessage(t, h, "success", "请确认合成安排？")
	body := digestConsentBody(h)
	for attempt := 0; attempt < 2; attempt++ {
		w := emailTestRequest(h, http.MethodPost, "/api/v1/digest/run", body, true)
		var response struct {
			Report         *store.DigestReport `json:"report"`
			SendingEnabled bool                `json:"sending_enabled"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Report == nil || response.SendingEnabled || response.Report.State != "succeeded" || response.Report.ID != store.DigestReportID(aiDay()) || response.Report.ConversationCount != 1 || len(response.Report.EvidenceIDs) != 1 || !strings.Contains(response.Report.Summary, "合成摘要") {
			t.Fatal("report did not reflect durable result", w.Code, w.Body.String())
		}
	}
	latest := emailTestRequest(h, http.MethodGet, "/api/v1/digest/latest", "", true)
	if latest.Code != 200 || !strings.Contains(latest.Body.String(), "合成摘要") {
		t.Fatal("successful response was not persisted", latest.Code, latest.Body.String())
	}
	if used, err := h.inbox.CountAICalls(aiDay()); err != nil || used != 1 || stub.calls.Load() != 1 {
		t.Fatal("daily retry repeated model work", used, stub.calls.Load(), err)
	}
}

func TestDigestProviderErrorsAreNotDeviceAuthenticationFailuresOrBlindRetries(t *testing.T) {
	for _, entry := range []struct {
		name   string
		err    error
		status int
		state  string
	}{
		{"provider-auth", &agent.Error{Kind: agent.AuthFailed}, 503, "failed"},
		{"invalid-output", &agent.Error{Kind: agent.InvalidOutput}, 502, "failed"},
		{"provider-unavailable", &agent.Error{Kind: agent.Unavailable}, 503, "unknown"},
		{"timeout", &agent.Error{Kind: agent.Timeout}, 504, "unknown"},
		{"cancelled", context.Canceled, 408, "unknown"},
		{"deadline", context.DeadlineExceeded, 504, "unknown"},
	} {
		t.Run(entry.name, func(t *testing.T) {
			stub := &digestStubAnalyzer{err: entry.err}
			h := newDigestTestHandler(t, stub)
			seedDigestMessage(t, h, "error", "请核对合成消息？")
			body := digestConsentBody(h)
			w := emailTestRequest(h, http.MethodPost, "/api/v1/digest/run", body, true)
			if w.Code != entry.status || strings.Contains(w.Body.String(), "DEVICE_UNAUTHORIZED") {
				t.Fatal("wrong error classification", w.Code, w.Body.String())
			}
			report, err := h.inbox.LatestDigestReport(context.Background())
			if err != nil || report == nil || report.State != entry.state || report.ErrorCode == "" {
				t.Fatal("failure state was lost", report, err)
			}
			w = emailTestRequest(h, http.MethodPost, "/api/v1/digest/run", body, true)
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"`+entry.state+`"`) || stub.calls.Load() != 1 {
				t.Fatal("uncertain or failed work replayed", w.Code, w.Body.String(), stub.calls.Load())
			}
			if used, err := h.inbox.CountAICalls(aiDay()); err != nil || used != 1 {
				t.Fatal("failed attempt not charged exactly once", used, err)
			}
		})
	}
}

func TestDigestSharesGateAndAssistantQuotaAndHonorsPause(t *testing.T) {
	stub := &digestStubAnalyzer{}
	h := newDigestTestHandler(t, stub)
	allowed := seedDigestMessage(t, h, "allowed", "请确认合成内容？")
	paused := seedDigestMessage(t, h, "paused", "PRIVATE_SYNTHETIC_PAUSED_TEXT 请确认？")
	if err := h.inbox.UpdateAISettings(paused, "paused", "natural"); err != nil {
		t.Fatal(err)
	}
	stub.inspect = func(input agent.Input) {
		if input.ConversationID != allowed {
			t.Error("paused source sent to model")
		}
		for _, message := range input.Messages {
			if strings.Contains(message.Body, "PRIVATE_SYNTHETIC_PAUSED_TEXT") {
				t.Error("paused text leaked")
			}
		}
	}
	h.ai.gate <- struct{}{}
	w := emailTestRequest(h, http.MethodPost, "/api/v1/digest/run", digestConsentBody(h), true)
	<-h.ai.gate
	if w.Code != 409 || stub.calls.Load() != 0 {
		t.Fatal("shared gate bypassed", w.Code, w.Body.String())
	}
	h.ai.dailyLimit = 1
	if _, err := h.inbox.ReserveAssistantCallContext(context.Background(), aiDay(), 1); err != nil {
		t.Fatal(err)
	}
	w = emailTestRequest(h, http.MethodPost, "/api/v1/digest/run", digestConsentBody(h), true)
	if w.Code != 429 || stub.calls.Load() != 0 {
		t.Fatal("assistant quota bypassed", w.Code, w.Body.String())
	}

	h2 := newDigestTestHandler(t, stub)
	allowed = seedDigestMessage(t, h2, "allowed", "请确认合成内容？")
	paused = seedDigestMessage(t, h2, "paused", "PRIVATE_SYNTHETIC_PAUSED_TEXT 请确认？")
	if err := h2.inbox.UpdateAISettings(paused, "paused", "natural"); err != nil {
		t.Fatal(err)
	}
	w = emailTestRequest(h2, http.MethodPost, "/api/v1/digest/run", digestConsentBody(h2), true)
	if w.Code != 200 || stub.calls.Load() != 1 || strings.Contains(w.Body.String(), "PRIVATE_SYNTHETIC_PAUSED_TEXT") {
		t.Fatal("pause handling or available run", w.Code, w.Body.String())
	}
	if _, err := h2.inbox.ReserveAssistantCallContext(context.Background(), aiDay(), 1); !errors.Is(err, store.ErrQuotaExceeded) {
		t.Fatal("digest quota invisible to assistant", err)
	}
}

func TestDigestScheduleEnablingNeedsModelConsentAndDisablingDoesNot(t *testing.T) {
	h := newEmailTestHandler(t)
	if w := emailTestRequest(h, http.MethodPatch, "/api/v1/digest/schedule", `{"enabled":true,"local_time":"09:30","timezone":"Asia/Shanghai","expected_revision":1,"confirmed":true}`, true); w.Code != 503 {
		t.Fatal("enabled unconfigured recurring disclosure", w.Code, w.Body.String())
	}
	h = newDigestTestHandler(t, &digestStubAnalyzer{})
	body := digestScheduleBody(h, true, 1)
	for _, invalid := range []string{strings.Replace(body, `"confirmed":true`, `"Confirmed":true`, 1), strings.Replace(body, `"enabled":true`, `"enabled":null`, 1), strings.Replace(body, `"expected_revision":1`, `"expected_revision":null`, 1)} {
		if w := emailTestRequest(h, http.MethodPatch, "/api/v1/digest/schedule", invalid, true); w.Code != 400 {
			t.Fatal("invalid recurring consent accepted", w.Code, w.Body.String())
		}
	}
	if w := emailTestRequest(h, http.MethodPatch, "/api/v1/digest/schedule", strings.Replace(body, `"model_config_revision":7`, `"model_config_revision":6`, 1), true); w.Code != 409 {
		t.Fatal("stale recurring model consent accepted", w.Code, w.Body.String())
	}
	if w := emailTestRequest(h, http.MethodPatch, "/api/v1/digest/schedule", body, true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	h.ai.client = nil
	w := emailTestRequest(h, http.MethodPatch, "/api/v1/digest/schedule", `{"enabled":false,"local_time":"09:30","timezone":"Asia/Shanghai","expected_revision":2,"confirmed":true}`, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":false`) || !strings.Contains(w.Body.String(), `"model_config_id":""`) {
		t.Fatal("disable depended on model authorization", w.Code, w.Body.String())
	}
}
