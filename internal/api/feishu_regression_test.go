package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/qilinbro/msg-manager/internal/agent"
	"github.com/qilinbro/msg-manager/internal/store"
)

func TestFeishuGateBusyDoesNotBurnWindowAndChildDeadlineIsUnknown(t *testing.T) {
	h, model, _, binding, _ := newFeishuTestFixture(t, true)
	h.ai.gate <- struct{}{}
	busy := emailTestRequest(h, "POST", feishuPrefix+"/preview", feishuPreviewBody(binding), true)
	<-h.ai.gate
	if busy.Code != 409 {
		t.Fatalf("busy: %d %s", busy.Code, busy.Body.String())
	}
	latest, err := h.inbox.LatestFeishuReport(context.Background())
	if err != nil || latest != nil || model.calls.Load() != 0 {
		t.Fatal("busy consumed natural window", err)
	}
	// The Analyze sub-call can time out independently while the 115s job is alive.
	model.failure = context.DeadlineExceeded
	started := feishuReportResponse(t, emailTestRequest(h, "POST", feishuPrefix+"/preview", feishuPreviewBody(binding), true))
	finished := waitFeishuReport(t, h, started.ID)
	if finished.State != "unknown" || finished.GeneratedAt == nil || model.calls.Load() != 1 {
		t.Fatal("child timeout was falsely known failed", finished.State)
	}
	again := emailTestRequest(h, "POST", feishuPrefix+"/preview", feishuPreviewBody(binding), true)
	if again.Code != 200 || feishuReportResponse(t, again).State != "unknown" || model.calls.Load() != 1 {
		t.Fatal("unknown window was retried")
	}
	used, err := h.inbox.CountAICallsContext(context.Background(), "2026-11-17")
	if err != nil || used != 1 {
		t.Fatal("failed actual call did not consume quota", used, err)
	}
}

func TestFeishuLocalRelevanceAndEmptySourcesStillBindModel(t *testing.T) {
	message := func(id, body string, unread bool) store.FeishuMessage {
		return store.FeishuMessage{DigestMessage: store.DigestMessage{ConversationID: id, Title: "合成", Unread: unread, Message: agent.Evidence{ID: id, Direction: "incoming", Body: body, OccurredAt: "2026-11-17T11:00:00.000000000Z"}}}
	}
	selected, groups := selectFeishuInput([]store.FeishuMessage{message("ordinary", "天气很好", false), message("unread", "天气很好", true), message("keyword", "请确认资料", false)})
	if len(groups) != 2 || len(selected.SourceRefs) != 2 || groups[0].Input.ConversationID != "unread" || groups[1].Input.ConversationID != "keyword" {
		t.Fatal("local relevance threshold lost")
	}
	h, model, _, binding, first := newFeishuTestFixture(t, false)
	read := true
	_, err := h.inbox.Import(store.ImportRequest{Platform: "qq", AccountID: "synthetic-profile-account", AccountName: "合成测试账号", Messages: []store.ImportMessage{{ExternalID: "ordinary-read", PeerID: "peer-one", PeerName: "合成", Direction: "incoming", Body: "天气很好", Read: &read, OccurredAt: "2026-11-17T11:00:00Z"}}})
	if err != nil {
		t.Fatal(err)
	}
	_ = first
	accepted := feishuReportResponse(t, emailTestRequest(h, "POST", feishuPrefix+"/preview", feishuPreviewBody(binding), true))
	report := waitFeishuReport(t, h, accepted.ID)
	if report.State != "succeeded" || model.calls.Load() != 0 || len(report.SourceRefs) != 0 || len(report.Excerpts) != 0 || !strings.Contains(report.Summary, "没有达到相关性阈值") {
		t.Fatalf("ordinary read chatter reached model/quote: %+v", report)
	}
	h.aiMu.Lock()
	h.modelConfig.Revision++
	h.aiMu.Unlock()
	denied := emailTestRequest(h, "GET", feishuPrefix+"/reports/"+report.ID, "", true)
	if denied.Code != 409 || strings.Contains(denied.Body.String(), report.Summary) {
		t.Fatal("empty-source report bypassed model revision binding", denied.Code)
	}
}

func TestAssistantFeishuPreviewNeedsOwnConfirmationAndNeverClaimsDelivery(t *testing.T) {
	h, model, _, _, first := newFeishuTestFixture(t, true)
	model.entered = make(chan agent.Input, 2)
	model.release = make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(model.release)
		}
	}()
	reportID := ""
	model.chat = func(_ context.Context, index int, _ agent.ToolInput) (agent.ToolResult, error) {
		switch index {
		case 1:
			return assistantFixtureCall("preview_once", "generate_feishu_preview", `{}`), nil
		case 2:
			return assistantFixtureCall("read_report", "get_feishu_report", feishuJSON(map[string]any{"report_id": reportID})), nil
		default:
			return assistantFixtureAnswer("只说明已读取合成飞书摘要，未发送。"), nil
		}
	}
	pending := assistantFixtureStart(t, h, "")
	if pending.State != "awaiting_confirmation" || pending.PendingAction == nil || model.calls.Load() != 0 {
		t.Fatalf("preview ran before independent confirmation: %+v", pending)
	}
	if strings.Contains(string(pending.PendingAction.Arguments), "ou_synthetic_own_private_target") || !strings.Contains(string(pending.PendingAction.Arguments), "window_end") {
		t.Fatal("pending preview lacks frozen window or exposed full recipient")
	}
	confirmed := assistantFixtureResponse(t, emailTestRequest(h, "POST", assistantPrefix+"/runs/"+pending.RunID+"/confirm", assistantFixtureResolveBody(pending, "confirm"), true))
	if confirmed.State != "completed" || len(confirmed.ToolResults) != 1 || confirmed.ToolResults[0].State != "pending" || model.chats.Load() != 1 || strings.Contains(confirmed.AssistantMessage, "已送达") {
		t.Fatalf("202 was reinterpreted as a completed operation: %+v", confirmed)
	}
	_ = receiveFeishuInput(t, model.entered)
	var envelope struct {
		Report store.FeishuReport `json:"report"`
	}
	if json.Unmarshal(confirmed.ToolResults[0].Data, &envelope) != nil || envelope.Report.State != "running" {
		t.Fatal("native tool did not retain truthful accepted report")
	}
	reportID = envelope.Report.ID
	close(model.release)
	released = true
	report := waitFeishuReport(t, h, reportID)
	if report.State != "succeeded" {
		t.Fatal("preview failed", report.State)
	}
	readRun := assistantFixtureStart(t, h, confirmed.SessionID)
	if readRun.State != "completed" || model.chats.Load() != 3 || len(readRun.ToolResults) != 1 || readRun.ToolResults[0].State != "succeeded" || !strings.Contains(string(readRun.ToolResults[0].Data), "合成合同") {
		t.Fatalf("report read/native continuation failed: %+v", readRun)
	}
	for _, mode := range []string{"paused", "draft_only"} {
		w := emailTestRequest(h, "PATCH", "/api/v1/conversations/"+first+"/ai-settings", feishuJSON(map[string]any{"mode": mode}), true)
		if w.Code != 200 {
			t.Fatalf("settings: %d %s", w.Code, w.Body.String())
		}
	}
	cached := emailTestRequest(h, "GET", assistantPrefix+"/runs/"+readRun.RunID, "", true)
	if cached.Code != 200 || strings.Contains(cached.Body.String(), "合成合同") || strings.Contains(cached.Body.String(), report.Summary) || model.chats.Load() != 3 {
		t.Fatal("cached Assistant run revived derived body after pause/resume", cached.Code, cached.Body.String())
	}
	stale := emailTestRequest(h, "GET", feishuPrefix+"/reports/"+reportID, "", true)
	if stale.Code != 409 {
		t.Fatal("source revision mismatch did not revoke report read", stale.Code)
	}
}

func TestFeishuScheduleDisableIsStrictUnboundRevocation(t *testing.T) {
	t.Run("HTTP zero binding keys are rejected even without credentials or model", func(t *testing.T) {
		h := newEmailTestHandler(t)
		schedule, err := h.inbox.LoadFeishuSchedule(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range feishuBindingFields {
			body := map[string]any{"enabled": false, "expected_revision": schedule.Revision, "confirmed": true}
			if strings.HasSuffix(key, "revision") {
				body[key] = 0
			} else {
				body[key] = ""
			}
			rejected := emailTestRequest(h, "PATCH", feishuPrefix+"/schedule", feishuJSON(body), true)
			if rejected.Code != 400 {
				t.Fatalf("disable accepted binding key %s: %d %s", key, rejected.Code, rejected.Body.String())
			}
		}
		stopped := emailTestRequest(h, "PATCH", feishuPrefix+"/schedule", feishuJSON(map[string]any{"enabled": false, "expected_revision": schedule.Revision, "confirmed": true}), true)
		if stopped.Code != 200 {
			t.Fatalf("unbound disable requires a configured model/recipient: %d %s", stopped.Code, stopped.Body.String())
		}
	})
	t.Run("Native disable prepares exactly the same three-key body", func(t *testing.T) {
		h, model, _, binding, _ := newFeishuTestFixture(t, false)
		deleted := emailTestRequest(h, "DELETE", feishuPrefix+"/config", feishuJSON(map[string]any{"expected_revision": binding.ConfigRevision, "confirmed": true}), true)
		if deleted.Code != 200 {
			t.Fatalf("delete fixture config: %d %s", deleted.Code, deleted.Body.String())
		}
		schedule, err := h.inbox.LoadFeishuSchedule(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		model.chat = func(_ context.Context, index int, _ agent.ToolInput) (agent.ToolResult, error) {
			if index == 1 {
				return assistantFixtureCall("disable_schedule", "update_feishu_schedule", feishuJSON(map[string]any{"enabled": false, "expected_revision": schedule.Revision})), nil
			}
			return assistantFixtureAnswer("已关闭后续飞书摘要计划，不声称撤回任何消息。"), nil
		}
		pending := assistantFixtureStart(t, h, "")
		if pending.PendingAction == nil {
			t.Fatalf("disable needs nonexistent Feishu credentials: %+v", pending)
		}
		h.assistant.mu.Lock()
		prepared := h.assistant.runs[pending.RunID].pending.prepared
		h.assistant.mu.Unlock()
		var body map[string]json.RawMessage
		if json.Unmarshal(prepared.Body, &body) != nil || len(body) != 3 || string(body["enabled"]) != "false" || string(body["confirmed"]) != "true" {
			t.Fatalf("Native disable body drift: %s", prepared.Body)
		}
		confirmed := assistantFixtureResponse(t, emailTestRequest(h, "POST", assistantPrefix+"/runs/"+pending.RunID+"/confirm", assistantFixtureResolveBody(pending, "confirm"), true))
		if confirmed.State != "completed" || len(confirmed.ToolResults) != 1 || confirmed.ToolResults[0].State != "succeeded" || model.calls.Load() != 0 {
			t.Fatalf("Native unbound disable failed: %+v", confirmed)
		}
	})
}
