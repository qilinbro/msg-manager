package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/qilinbro/msg-manager/internal/agent"
	"github.com/qilinbro/msg-manager/internal/connector/feishu"
	"github.com/qilinbro/msg-manager/internal/store"
)

func TestFeishuScheduledGenerationRechecksScheduleEpoch(t *testing.T) {
	for _, tc := range []struct {
		name        string
		withMessage bool
		change      string
		wantCalls   int32
	}{
		{"disabled before first model dispatch", true, "disable", 0},
		{"same binding re-enabled after revocation", true, "reenable", 0},
		{"same enabled binding saved with new revision", true, "resave", 0},
		{"disabled before empty report completion", false, "disable", 0},
		{"unchanged authorization permits dispatch", true, "", 1},
		{"unchanged authorization permits empty report", false, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, model, sender, binding, _ := newFeishuTestFixture(t, tc.withMessage)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			now := time.Date(2026, 11, 17, 11, 59, 30, 0, time.UTC)
			h.feishu.now = func() time.Time { return now }
			setSchedule := func(enabled bool, expected int64) store.FeishuSchedule {
				t.Helper()
				input := map[string]any{"enabled": enabled, "expected_revision": expected, "confirmed": true}
				if enabled {
					input["model_config_id"] = binding.ModelConfigID
					input["model_config_revision"] = binding.ModelConfigRevision
					input["config_revision"] = binding.ConfigRevision
					input["target_binding_id"] = binding.TargetBindingID
				}
				w := emailTestRequest(h, "PATCH", feishuPrefix+"/schedule", feishuJSON(input), true)
				var schedule store.FeishuSchedule
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &schedule) != nil {
					t.Fatalf("schedule acknowledgement: %d %s", w.Code, w.Body.String())
				}
				return schedule
			}
			schedule, err := h.inbox.LoadFeishuSchedule(ctx)
			if err != nil {
				t.Fatal(err)
			}
			schedule = setSchedule(true, schedule.Revision)
			now = now.Add(time.Minute)
			start, end := store.FeishuWindow(now)
			messages, err := h.inbox.LoadFeishuMessages(ctx, store.FeishuTime(start), store.FeishuTime(end), 501)
			if err != nil {
				t.Fatal(err)
			}
			report, groups := selectFeishuInput(messages)
			report.ID = store.FeishuReportID(store.FeishuTime(end))
			report.WindowStart, report.WindowEnd = store.FeishuTime(start), store.FeishuTime(end)
			report.State, report.Trigger = "running", "schedule"
			report.FeishuBinding, report.ScheduleRevision = binding, schedule.Revision
			report.CreatedAt = store.FeishuTime(now)
			if claimed, err := h.inbox.ClaimFeishuReport(ctx, report, now); err != nil || !claimed {
				t.Fatal("scheduled claim", claimed, err)
			}
			switch tc.change {
			case "disable":
				setSchedule(false, schedule.Revision)
			case "reenable":
				disabled := setSchedule(false, schedule.Revision)
				setSchedule(true, disabled.Revision)
			case "resave":
				setSchedule(true, schedule.Revision)
			}
			// White-box the accepted goroutine delayed past the writer barrier.
			// Its uncancelled context models registration after the first cancellation
			// snapshot. Authorization must be rechecked without relying on timing,
			// cancellation delivery, sleeps, real providers or real platform IO.
			if ctx.Err() != nil {
				t.Fatal("fixture expired before worker dispatch", ctx.Err())
			}
			h.generateFeishuReport(ctx, h.ai, report, groups, "")
			finished, err := h.inbox.FeishuReportByID(ctx, report.ID)
			if err != nil || finished == nil {
				t.Fatal("terminal report", err)
			}
			if tc.change != "" {
				if finished.State != "failed" || finished.ErrorCode == "" || finished.Summary != "" || finished.Text != "" || finished.ContentHash != "" || len(finished.Todos) != 0 || len(finished.Excerpts) != 0 || finished.Delivery != nil {
					t.Fatal("revoked epoch produced a report body or delivery", finished.State, finished.ErrorCode)
				}
				if sender.calls.Load() != 0 {
					t.Fatal("revoked epoch dispatched to Feishu", sender.calls.Load())
				}
			} else {
				if finished.State != "succeeded" || finished.Delivery == nil {
					t.Fatal("unchanged scheduled authorization was rejected", finished.State)
				}
				if delivery := waitFeishuDelivery(t, h, finished.Delivery.ID); delivery.State != "succeeded" || sender.calls.Load() != 1 {
					t.Fatal("unchanged schedule did not dispatch exactly once", delivery.State, sender.calls.Load())
				}
			}
			used, err := h.inbox.CountAICallsContext(ctx, "2026-11-17")
			if err != nil || model.calls.Load() != tc.wantCalls || used != int(tc.wantCalls) {
				t.Fatal("model dispatch/quota", model.calls.Load(), used, err)
			}
		})
	}
}

func TestFeishuScheduleRevocationCancelsLateRegisteredJobs(t *testing.T) {
	h, model, sender, _, _ := newFeishuTestFixture(t, false)
	schedule, err := h.inbox.LoadFeishuSchedule(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	beforeCtx, finishBefore, err := h.feishu.begin("probe:before-cancel", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer finishBefore()
	h.aiMu.RLock()
	h.feishuDispatchMu.RLock()
	leased := true
	defer func() {
		if leased {
			h.feishuDispatchMu.RUnlock()
			h.aiMu.RUnlock()
		}
	}()
	reply := make(chan int, 1)
	go func() {
		reply <- emailTestRequest(h, "PATCH", feishuPrefix+"/schedule", feishuJSON(map[string]any{"enabled": false, "expected_revision": schedule.Revision, "confirmed": true}), true).Code
	}()
	select {
	case <-beforeCtx.Done():
		if beforeCtx.Err() != context.Canceled {
			t.Fatal("first cancellation was not observed", beforeCtx.Err())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("revocation did not cancel jobs before waiting for the writer barrier")
	}
	// begin serializes on the same runtime mutex as cancelOperations, so this
	// registration is deterministically later than the first cancellation
	// snapshot, while the dispatch reader still keeps the writer from entering.
	afterCtx, finishAfter, err := h.feishu.begin("probe:after-cancel", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer finishAfter()
	leased = false
	h.feishuDispatchMu.RUnlock()
	h.aiMu.RUnlock()
	select {
	case status := <-reply:
		if status != 200 {
			t.Fatal("revocation acknowledgement", status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("revocation did not acquire the released writer barrier")
	}
	if afterCtx.Err() != context.Canceled || model.calls.Load() != 0 || sender.calls.Load() != 0 {
		t.Fatal("late registered job survived the revocation acknowledgement", afterCtx.Err())
	}
}

func TestFeishuMutationAcknowledgementDrainsExternalDispatch(t *testing.T) {
	t.Run("pause cancels in-flight Analyze and prevents next conversation", func(t *testing.T) {
		h, model, sender, binding, first := newFeishuTestFixture(t, true)
		_, err := h.inbox.Import(store.ImportRequest{Platform: "qq", AccountID: "synthetic-profile-account", AccountName: "合成测试账号", Messages: []store.ImportMessage{{ExternalID: "second-window-message", PeerID: "peer-two", PeerName: "另一合成联系人", Body: "请确认下一份资料", Direction: "incoming", OccurredAt: "2026-11-17T11:01:00Z"}}})
		if err != nil {
			t.Fatal(err)
		}
		model.entered = make(chan agent.Input, 4)
		model.release = make(chan struct{})
		defer close(model.release)
		started := feishuReportResponse(t, emailTestRequest(h, "POST", feishuPrefix+"/preview", feishuPreviewBody(binding), true))
		_ = receiveFeishuInput(t, model.entered)
		paused := emailTestRequest(h, "PATCH", "/api/v1/conversations/"+first+"/ai-settings", `{"mode":"paused"}`, true)
		if paused.Code != 200 {
			t.Fatalf("pause acknowledgement: %d %s", paused.Code, paused.Body.String())
		}
		finished := waitFeishuReport(t, h, started.ID)
		if finished.State != "unknown" || model.calls.Load() != 1 || sender.calls.Load() != 0 {
			t.Fatal("privacy acknowledgement allowed further external dispatch", finished.State, model.calls.Load(), sender.calls.Load())
		}
		used, err := h.inbox.CountAICallsContext(context.Background(), "2026-11-17")
		if err != nil || used != 1 {
			t.Fatal("cancelled actual call quota", used, err)
		}
		for _, mode := range []string{"draft_only", "paused"} {
			w := emailTestRequest(h, "PATCH", "/api/v1/conversations/"+first+"/ai-settings", feishuJSON(map[string]any{"mode": mode}), true)
			if w.Code != 200 {
				t.Fatal("settings revision", w.Code)
			}
		}
		if denied := emailTestRequest(h, "GET", feishuPrefix+"/reports/"+started.ID, "", true); denied.Code != 409 {
			t.Fatal("changed revision report leaked", denied.Code)
		}
	})
	t.Run("configuration save cancels and drains send before acknowledgement", func(t *testing.T) {
		h, model, sender, binding, _ := newFeishuTestFixture(t, true)
		started := feishuReportResponse(t, emailTestRequest(h, "POST", feishuPrefix+"/preview", feishuPreviewBody(binding), true))
		report := waitFeishuReport(t, h, started.ID)
		if report.State != "succeeded" {
			t.Fatal("fixture report", report.State)
		}
		sender.entered = make(chan feishu.SendInput, 2)
		sender.release = make(chan struct{})
		defer close(sender.release)
		accepted := emailTestRequest(h, "POST", feishuPrefix+"/reports/"+report.ID+"/send", feishuSendBody(report), true)
		var envelope struct {
			Delivery store.FeishuDelivery `json:"delivery"`
		}
		_ = json.Unmarshal(accepted.Body.Bytes(), &envelope)
		if accepted.Code != 202 {
			t.Fatalf("send acceptance: %d %s", accepted.Code, accepted.Body.String())
		}
		select {
		case <-sender.entered:
		case <-time.After(3 * time.Second):
			t.Fatal("send was not dispatched")
		}
		changed := emailTestRequest(h, "PATCH", feishuPrefix+"/config", feishuJSON(map[string]any{"expected_revision": binding.ConfigRevision, "app_id": "cli_synthetic_bot", "app_secret": "", "retain_secret": true, "recipient_open_id": "ou_synthetic_new_private_target", "confirmed": true}), true)
		if changed.Code != 200 {
			t.Fatalf("config acknowledgement: %d %s", changed.Code, changed.Body.String())
		}
		delivery := waitFeishuDelivery(t, h, envelope.Delivery.ID)
		if delivery.State != "unknown" || sender.calls.Load() != 1 || model.calls.Load() != 1 {
			t.Fatal("send cancellation was misrepresented or repeated", delivery.State)
		}
		again := emailTestRequest(h, "POST", feishuPrefix+"/reports/"+report.ID+"/send", feishuSendBody(report), true)
		if again.Code != 200 || sender.calls.Load() != 1 {
			t.Fatal("old ledger replayed after new configuration", again.Code)
		}
	})
}
