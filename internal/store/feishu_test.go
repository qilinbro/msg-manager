package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// All fixtures are synthetic and every database is an encrypted t.TempDir file.
func feishuTestTime(t *testing.T, raw string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func feishuTestBinding(t *testing.T, s *Store) (FeishuConfig, FeishuBinding) {
	t.Helper()
	ctx := context.Background()
	current, err := s.LoadFeishuConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	current, err = s.SaveFeishuConfig(ctx, FeishuConfig{AppID: "cli_synthetic_app", AppSecret: "synthetic-feishu-private-secret", RecipientOpenID: "ou_synthetic_self"}, current.Revision, false)
	if err != nil {
		t.Fatal(err)
	}
	return current, FeishuBinding{ModelConfigID: strings.Repeat("a", 64), ModelConfigRevision: 0, ConfigRevision: current.Revision, TargetBindingID: current.TargetBindingID()}
}

func feishuTestReport(t *testing.T, b FeishuBinding, now time.Time, refs ...FeishuSourceRef) FeishuReport {
	t.Helper()
	start, end := FeishuWindow(now)
	return FeishuReport{ID: FeishuReportID(FeishuTime(end)), WindowStart: FeishuTime(start), WindowEnd: FeishuTime(end), FeishuBinding: b, Trigger: "manual", SourceRefs: refs, CreatedAt: FeishuTime(now)}
}

func feishuTestComplete(t *testing.T, s *Store, r FeishuReport, now time.Time) FeishuReport {
	t.Helper()
	r.State = "succeeded"
	r.Summary = "合成摘要：请人工核对。"
	r.Text = "飞书早晚报\n合成摘要：请人工核对。"
	r.Todos = []FeishuTodo{}
	r.Excerpts = []FeishuExcerpt{}
	at := FeishuTime(now)
	r.GeneratedAt = &at
	r.ContentHash = FeishuContentHash(r)
	if err := s.CompleteFeishuReport(context.Background(), r); err != nil {
		t.Fatal("complete synthetic report", err)
	}
	return r
}

func TestFeishuFixedShanghaiWindowsAndBoundContentHash(t *testing.T) {
	for _, tc := range []struct{ now, end string }{
		{"2026-11-17T07:59:59.999999999+08:00", "2026-11-16T12:00:00Z"},
		{"2026-11-17T08:00:00+08:00", "2026-11-17T00:00:00Z"},
		{"2026-11-17T19:59:59.999999999+08:00", "2026-11-17T00:00:00Z"},
		{"2026-11-17T20:00:00+08:00", "2026-11-17T12:00:00Z"},
		{"2026-11-18T00:00:00+08:00", "2026-11-17T12:00:00Z"},
		{"2026-11-17T12:00:00Z", "2026-11-17T12:00:00Z"},
	} {
		now := feishuTestTime(t, tc.now)
		start, end := FeishuWindow(now)
		if FeishuTime(end) != tc.end || end.Sub(start) != 12*time.Hour || !feishuWindowValid(FeishuTime(start), FeishuTime(end)) {
			t.Fatalf("wrong Shanghai window for %s", tc.now)
		}
		if next := FeishuNextWindow(now); !next.After(now) || next.Sub(end) != 12*time.Hour {
			t.Fatalf("next slot is not strictly future for %s", tc.now)
		}
		if len(FeishuReportID(tc.end)) != 35 || len(FeishuDeliveryID(tc.end)) != 35 || FeishuReportID(tc.end) == FeishuDeliveryID(tc.end) {
			t.Fatal("invalid deterministic IDs")
		}
	}
	b := FeishuBinding{ModelConfigID: strings.Repeat("a", 64), ConfigRevision: 2, TargetBindingID: strings.Repeat("b", 64)}
	r := feishuTestReport(t, b, feishuTestTime(t, "2026-11-17T08:00:00+08:00"))
	r.Text = "合成正文🙂"
	base := FeishuContentHash(r)
	for _, change := range []func(*FeishuReport){
		func(v *FeishuReport) { v.Text += "！" },
		func(v *FeishuReport) { v.ID += "0" },
		func(v *FeishuReport) { v.WindowEnd = "2026-11-17T12:00:00Z" },
		func(v *FeishuReport) { v.ConfigRevision++ },
		func(v *FeishuReport) { v.ModelConfigRevision++ },
		func(v *FeishuReport) { v.TargetBindingID = strings.Repeat("c", 64) },
	} {
		copy := r
		change(&copy)
		if FeishuContentHash(copy) == base {
			t.Fatal("hash failed to bind report/window/text/configuration")
		}
	}
}

func TestFeishuConfigCASRetentionAndEverySaveRevokesSchedule(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := feishuTestTime(t, "2026-11-17T08:00:00+08:00")
	initial, err := s.LoadFeishuConfig(ctx)
	if err != nil || initial.Configured() || initial.Revision != 1 {
		t.Fatal("config not default-off", err)
	}
	schedule, err := s.LoadFeishuSchedule(ctx)
	if err != nil || schedule.Enabled || schedule.Revision != 1 || schedule.ConfigRevision != 0 || schedule.NextWindowEnd != nil || schedule.Timezone != FeishuTimezone || strings.Join(schedule.Slots, ",") != "08:00,20:00" {
		t.Fatal("schedule not default-off", schedule, err)
	}
	config, binding := feishuTestBinding(t, s)
	schedule, _ = s.LoadFeishuSchedule(ctx)
	schedule, err = s.SaveFeishuSchedule(ctx, FeishuSchedule{Enabled: true, FeishuBinding: binding}, schedule.Revision, now, "synthetic-owner")
	if err != nil || !schedule.Enabled || schedule.NextWindowEnd == nil || *schedule.NextWindowEnd != "2026-11-17T12:00:00Z" {
		t.Fatal("enable did not choose future slot", schedule, err)
	}
	if _, err := s.SaveFeishuConfig(ctx, config, 1, false); !errors.Is(err, ErrStaleContext) {
		t.Fatal("stale config save accepted", err)
	}
	before := schedule.Revision
	config, err = s.SaveFeishuConfig(ctx, config, config.Revision, false)
	if err != nil {
		t.Fatal(err)
	}
	schedule, _ = s.LoadFeishuSchedule(ctx)
	if schedule.Enabled || schedule.Revision != before+1 || schedule.ConfigRevision != 0 {
		t.Fatal("same-value save retained recurring consent", schedule)
	}
	if err := s.CheckFeishuBinding(ctx, binding); !errors.Is(err, ErrFeishuConfigChanged) {
		t.Fatal("old revision survived same-value save", err)
	}
	bad := config
	bad.AppID = "cli_other_app"
	bad.AppSecret = ""
	if _, err := s.SaveFeishuConfig(ctx, bad, config.Revision, true); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("cross-app secret retention accepted", err)
	}
	if _, err := s.SaveFeishuConfig(ctx, config, config.Revision, true); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("retain plus explicit secret accepted", err)
	}
	retained := config
	retained.AppSecret = ""
	if _, err := s.SaveFeishuConfig(ctx, retained, config.Revision, false); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("empty secret implicitly retained", err)
	}
	retained, err = s.SaveFeishuConfig(ctx, retained, config.Revision, true)
	if err != nil || retained.AppSecret != config.AppSecret || retained.Revision != config.Revision+1 {
		t.Fatal("explicit same-app retention failed", err)
	}
	for _, encoded := range []string{fmt.Sprintf("%+v", retained), fmt.Sprintf("%#v", retained), func() string { raw, _ := json.Marshal(retained); return string(raw) }()} {
		if strings.Contains(encoded, retained.AppSecret) || strings.Contains(encoded, retained.RecipientOpenID) {
			t.Fatal("sensitive config formats reflect private values")
		}
	}
	binding.ConfigRevision = retained.Revision
	schedule, _ = s.LoadFeishuSchedule(ctx)
	schedule, err = s.SaveFeishuSchedule(ctx, FeishuSchedule{Enabled: true, FeishuBinding: binding}, schedule.Revision, now, "synthetic-owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveModelConfig(ctx, ModelConfig{BaseURL: "https://synthetic.invalid/v1", Model: "synthetic-model", APIKey: "synthetic-model-secret", DailyLimit: 50}, 0); err != nil {
		t.Fatal(err)
	}
	afterModel, _ := s.LoadFeishuSchedule(ctx)
	if afterModel.Enabled || afterModel.Revision != schedule.Revision+1 {
		t.Fatal("model save retained standing consent", afterModel)
	}
	if _, err := s.SaveFeishuSchedule(ctx, FeishuSchedule{}, schedule.Revision, now, ""); !errors.Is(err, ErrStaleContext) {
		t.Fatal("stale schedule save accepted", err)
	}
	// Disabling does not need any model binding, quota, sender or credentials.
	disabled, err := s.SaveFeishuSchedule(ctx, FeishuSchedule{}, afterModel.Revision, now, "")
	if err != nil || disabled.Enabled || disabled.ConfigRevision != 0 {
		t.Fatal("independent disable failed", err)
	}
	if err := s.DeleteFeishuConfig(ctx, retained.Revision); err != nil {
		t.Fatal(err)
	}
	cleared, _ := s.LoadFeishuConfig(ctx)
	if cleared.Configured() || cleared.AppSecret != "" || cleared.RecipientOpenID != "" || cleared.Revision != retained.Revision+1 {
		t.Fatal("clear did not erase stored credentials")
	}
}

func TestFeishuConcurrentNaturalWindowClaimsAndNoRetargetReplay(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	config, binding := feishuTestBinding(t, s)
	now := feishuTestTime(t, "2026-11-17T08:00:01+08:00")
	r := feishuTestReport(t, binding, now)
	var claims atomic.Int32
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, err := s.ClaimFeishuReport(ctx, r, now)
			if err != nil {
				t.Error(err)
			} else if claimed {
				claims.Add(1)
			}
		}()
	}
	wg.Wait()
	if claims.Load() != 1 {
		t.Fatal("report window not unique", claims.Load())
	}
	r = feishuTestComplete(t, s, r, now.Add(time.Second))
	loaded, err := s.FeishuReportByID(ctx, r.ID)
	if err != nil || loaded == nil || loaded.ID != r.ID || loaded.ContentHash != r.ContentHash || loaded.State != "succeeded" {
		t.Fatal("fr_ report could not be read", err)
	}
	claims.Store(0)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, claimed, err := s.ClaimFeishuDelivery(ctx, r.ID, r.ContentHash, binding, now.Add(2*time.Second), false)
			if err != nil {
				t.Error(err)
			} else if claimed {
				claims.Add(1)
			}
		}()
	}
	wg.Wait()
	if claims.Load() != 1 {
		t.Fatal("delivery window not unique", claims.Load())
	}
	loaded, err = s.FeishuReportByID(ctx, r.ID)
	if err != nil || loaded == nil || loaded.Delivery == nil || loaded.Delivery.ID != FeishuDeliveryID(r.WindowEnd) {
		t.Fatal("lost send response not recoverable from report", err)
	}
	d := *loaded.Delivery
	d.State = "unknown"
	d.ErrorCode = "FEISHU_INTERRUPTED"
	at := FeishuTime(now.Add(3 * time.Second))
	d.CompletedAt = &at
	if err := s.CompleteFeishuDelivery(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteFeishuDelivery(ctx, d); !errors.Is(err, ErrStaleContext) {
		t.Fatal("terminal delivery rewritten", err)
	}
	config.RecipientOpenID = "ou_other_declared_self"
	config, err = s.SaveFeishuConfig(ctx, config, config.Revision, false)
	if err != nil {
		t.Fatal(err)
	}
	newBinding := binding
	newBinding.ConfigRevision = config.Revision
	newBinding.TargetBindingID = config.TargetBindingID()
	changed := r
	changed.FeishuBinding = newBinding
	if claimed, err := s.ClaimFeishuReport(ctx, changed, now.Add(time.Minute)); err != nil || claimed {
		t.Fatal("configuration change regenerated natural window", claimed, err)
	}
	// Even after expiry and target/model changes the existing receipt wins;
	// no new token/message request can be inferred from a retry-like call.
	recovered, claimed, err := s.ClaimFeishuDelivery(ctx, r.ID, strings.Repeat("c", 64), newBinding, now.Add(25*time.Hour), false)
	if err != nil || claimed || recovered.ID != d.ID || recovered.State != "unknown" {
		t.Fatal("unknown delivery replayed after target change or expiry", claimed, err)
	}
	if got, err := s.FeishuDeliveryByID(ctx, d.ID); err != nil || got == nil || got.State != "unknown" || got.MessageID != "" {
		t.Fatal("fd_ minimal receipt unavailable", err)
	}
}

func TestFeishuRecoveryQuotaSharedAndPersistent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "synthetic-feishu.db")
	s, err := Open(path, testKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	_, b := feishuTestBinding(t, s)
	conversation := aiTestConversation(t, s, "qq", "feishu-quota-account", "feishu-quota-peer")
	now := feishuTestTime(t, "2026-11-17T08:00:01+08:00")
	r := feishuTestReport(t, b, now)
	if claimed, err := s.ClaimFeishuReport(ctx, r, now); err != nil || !claimed {
		t.Fatal("claim", claimed, err)
	}
	r = feishuTestComplete(t, s, r, now.Add(time.Second))
	d, claimed, err := s.ClaimFeishuDelivery(ctx, r.ID, r.ContentHash, b, now.Add(2*time.Second), false)
	if err != nil || !claimed {
		t.Fatal("claim delivery", claimed, err)
	}
	running := feishuTestReport(t, b, now.Add(12*time.Hour))
	if claimed, err := s.ClaimFeishuReport(ctx, running, now.Add(12*time.Hour)); err != nil || !claimed {
		t.Fatal("claim interrupted report", claimed, err)
	}
	var accepted, rejected atomic.Int32
	var wg sync.WaitGroup
	for i := range 30 {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			var err error
			switch index % 3 {
			case 0:
				_, err = s.ReserveFeishuCallContext(ctx, "2026-11-17", 7)
			case 1:
				_, err = s.ReserveAssistantCallContext(ctx, "2026-11-17", 7)
			default:
				_, err = s.ReserveAICallContext(ctx, "2026-11-17", conversation, 7)
			}
			if err == nil {
				accepted.Add(1)
			} else if errors.Is(err, ErrQuotaExceeded) {
				rejected.Add(1)
			} else {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if accepted.Load() != 7 || rejected.Load() != 23 {
		t.Fatal("model attempts did not share atomic quota", accepted.Load(), rejected.Load())
	}
	if count, err := s.CountAICalls("2026-11-17"); err != nil || count != 7 {
		t.Fatal("shared usage", count, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path, testKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReserveFeishuCallContext(ctx, "2026-11-17", 7); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatal("restart refunded failed/unfinished attempts", err)
	}
	if err := s.RecoverFeishuRunning(ctx, now.Add(13*time.Hour)); err != nil {
		t.Fatal(err)
	}
	gotReport, err := s.FeishuReportByID(ctx, running.ID)
	if err != nil || gotReport == nil || gotReport.State != "unknown" || gotReport.Text != "" || gotReport.ErrorCode != "FEISHU_INTERRUPTED" {
		t.Fatal("running report not conservatively recovered", err)
	}
	gotDelivery, err := s.FeishuDeliveryByID(ctx, d.ID)
	if err != nil || gotDelivery == nil || gotDelivery.State != "unknown" || gotDelivery.ErrorCode != "FEISHU_INTERRUPTED" {
		t.Fatal("running delivery not conservatively recovered", err)
	}
	if _, claimed, err := s.ClaimFeishuDelivery(ctx, r.ID, r.ContentHash, b, now.Add(26*time.Hour), false); err != nil || claimed {
		t.Fatal("recovery permitted another delivery", claimed, err)
	}
	if _, err := s.ReserveFeishuCallContext(ctx, "2026-11-18", 7); err != nil {
		t.Fatal("next natural day did not reset usage", err)
	}
}

func TestFeishuHalfOpenSourcesPauseResumeDeletionAndInputBound(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, b := feishuTestBinding(t, s)
	now := feishuTestTime(t, "2026-11-17T08:00:01+08:00")
	start, end := FeishuWindow(now)
	first := aiTestConversation(t, s, "qq", "feishu-source-account", "first")
	second := aiTestConversation(t, s, "qq", "feishu-source-account", "second")
	paused := aiTestConversation(t, s, "qq", "feishu-source-account", "paused")
	if err := s.UpdateAISettings(paused, "paused", ""); err != nil {
		t.Fatal(err)
	}
	mustImport(t, s, ImportRequest{Platform: "qq", AccountID: "feishu-source-account", Messages: []ImportMessage{
		importMessage("before", "first", "不在窗口：请排除", FeishuTime(start.Add(-time.Nanosecond))),
		importMessage("at-start", "first", "窗口起点：请核对", FeishuTime(start)),
		importMessage("before-end", "first", "窗口末端：请核对", FeishuTime(end.Add(-time.Nanosecond))),
		importMessage("at-end", "first", "下一窗口：请排除", FeishuTime(end)),
		importMessage("other-source", "second", "第二来源：需要核对", FeishuTime(end.Add(-time.Minute))),
		importMessage("paused-source", "paused", "暂停来源：请排除", FeishuTime(end.Add(-time.Minute))),
	}})
	messages, err := s.LoadFeishuMessages(ctx, FeishuTime(start), FeishuTime(end), 501)
	if err != nil || len(messages) != 3 {
		t.Fatal("half-open or paused source selection incorrect", len(messages), err)
	}
	for _, m := range messages {
		if m.ConversationID == paused || strings.Contains(m.Message.Body, "请排除") {
			t.Fatal("paused/outside message entered model candidates")
		}
	}
	refs := []FeishuSourceRef{{ConversationID: first, SettingsRevision: 0}, {ConversationID: second, SettingsRevision: 0}}
	if err := s.CheckFeishuSourceRefs(ctx, refs); err != nil {
		t.Fatal(err)
	}
	r := feishuTestReport(t, b, now, refs...)
	if claimed, err := s.ClaimFeishuReport(ctx, r, now); err != nil || !claimed {
		t.Fatal("claim sources", claimed, err)
	}
	// Neither source appears in excerpts: all model inputs still remain bound.
	r = feishuTestComplete(t, s, r, now.Add(time.Second))
	if err := s.UpdateAISettings(second, "paused", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateAISettings(second, "draft_only", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckFeishuSourceRefs(ctx, refs); !errors.Is(err, ErrFeishuSourceChanged) {
		t.Fatal("pause/resume revived old source revision", err)
	}
	if _, _, err := s.ClaimFeishuDelivery(ctx, r.ID, r.ContentHash, b, now.Add(2*time.Second), false); !errors.Is(err, ErrFeishuSourceChanged) {
		t.Fatal("non-excerpt source pause did not prevent sending", err)
	}
	// Simulate a future source-removal workflow in this temporary database.
	// Existing message foreign keys intentionally require removing its messages first.
	if _, err := s.db.Exec(`DELETE FROM messages WHERE workspace_id=? AND conversation_id=?`, workspaceID, first); err != nil {
		t.Fatal("delete synthetic source messages", err)
	}
	if _, err := s.db.Exec(`DELETE FROM conversations WHERE workspace_id=? AND id=?`, workspaceID, first); err != nil {
		t.Fatal("delete synthetic source", err)
	}
	old, err := s.FeishuReportByID(ctx, r.ID)
	if err != nil || old == nil || len(old.SourceRefs) != 2 {
		t.Fatal("source deletion cascaded away privacy evidence", err)
	}
	if err := s.CheckFeishuSourceRefs(ctx, old.SourceRefs); !errors.Is(err, ErrFeishuSourceChanged) {
		t.Fatal("deleted source passed validation", err)
	}
	batch := ImportRequest{Platform: "qq", AccountID: "feishu-source-account"}
	for i := 0; i < 505; i++ {
		batch.Messages = append(batch.Messages, importMessage(fmt.Sprintf("bounded-%03d", i), "second", "合成输入边界", FeishuTime(start.Add(time.Duration(i+1)*time.Second))))
	}
	mustImport(t, s, batch)
	messages, err = s.LoadFeishuMessages(ctx, FeishuTime(start), FeishuTime(end), 501)
	if err != nil || len(messages) != 501 {
		t.Fatal("candidate query not bounded", len(messages), err)
	}
	if _, err := s.LoadFeishuMessages(ctx, FeishuTime(start), FeishuTime(end), 502); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("oversized input limit accepted", err)
	}
}

func TestFeishuScheduleStartupGraceAndNoCatchup(t *testing.T) {
	for _, tc := range []struct {
		name          string
		elapsed       time.Duration
		startup, skip bool
	}{
		{"runtime-in-grace", time.Minute, false, false},
		{"runtime-at-grace", FeishuScheduleGrace, false, false},
		{"runtime-after-grace", FeishuScheduleGrace + time.Nanosecond, false, true},
		{"startup-no-catchup", time.Second, true, true},
		{"resume-after-day", 25 * time.Hour, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t)
			ctx := context.Background()
			_, b := feishuTestBinding(t, s)
			before := feishuTestTime(t, "2026-11-17T07:00:00+08:00")
			schedule, _ := s.LoadFeishuSchedule(ctx)
			schedule, err := s.SaveFeishuSchedule(ctx, FeishuSchedule{Enabled: true, FeishuBinding: b}, schedule.Revision, before, "synthetic-owner")
			if err != nil {
				t.Fatal(err)
			}
			due := feishuTestTime(t, "2026-11-17T08:00:00+08:00")
			now := due.Add(tc.elapsed)
			if err := s.SkipFeishuBacklog(ctx, now, tc.startup); err != nil {
				t.Fatal(err)
			}
			after, _ := s.LoadFeishuSchedule(ctx)
			if after.NextWindowEnd == nil {
				t.Fatal("enabled schedule lost cursor")
			}
			if tc.skip {
				if *after.NextWindowEnd != FeishuTime(FeishuNextWindow(now)) {
					t.Fatal("backlog did not advance to future", after)
				}
				if report, err := s.LatestFeishuReport(ctx); err != nil || report != nil {
					t.Fatal("skip generated a report", err)
				}
				return
			}
			if *after.NextWindowEnd != FeishuTime(due) {
				t.Fatal("allowed grace skipped current window", after)
			}
			r := feishuTestReport(t, b, now)
			r.Trigger = "schedule"
			r.ScheduleRevision = schedule.Revision
			if claimed, err := s.ClaimFeishuReport(ctx, r, now); err != nil || !claimed {
				t.Fatal("in-grace claim", claimed, err)
			}
			after, _ = s.LoadFeishuSchedule(ctx)
			if after.LastWindowEnd == nil || *after.LastWindowEnd != r.WindowEnd || after.NextWindowEnd == nil || *after.NextWindowEnd != FeishuTime(due.Add(12*time.Hour)) {
				t.Fatal("claim did not atomically advance cursor", after)
			}
			if claimed, err := s.ClaimFeishuReport(ctx, r, now); !errors.Is(err, ErrStaleContext) || claimed {
				t.Fatal("same schedule slot claimed twice", claimed, err)
			}
			r = feishuTestComplete(t, s, r, now.Add(time.Second))
			if _, claimed, err := s.ClaimFeishuDelivery(ctx, r.ID, r.ContentHash, b, now.Add(2*time.Second), true); err != nil || !claimed {
				t.Fatal("bound scheduled send rejected", claimed, err)
			}
		})
	}
}

func TestFeishuManualReportBlocksScheduledReuseAndExpiry(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, b := feishuTestBinding(t, s)
	now := feishuTestTime(t, "2026-11-17T08:00:00+08:00")
	schedule, _ := s.LoadFeishuSchedule(ctx)
	schedule, err := s.SaveFeishuSchedule(ctx, FeishuSchedule{Enabled: true, FeishuBinding: b}, schedule.Revision, now.Add(-time.Hour), "synthetic-owner")
	if err != nil {
		t.Fatal(err)
	}
	r := feishuTestReport(t, b, now)
	if claimed, err := s.ClaimFeishuReport(ctx, r, now); err != nil || !claimed {
		t.Fatal("manual claim", claimed, err)
	}
	r = feishuTestComplete(t, s, r, now.Add(time.Second))
	if _, _, err := s.ClaimFeishuDelivery(ctx, r.ID, r.ContentHash, b, now.Add(2*time.Second), true); !errors.Is(err, ErrStaleContext) {
		t.Fatal("manual preview implicitly authorized recurring delivery", err)
	}
	scheduled := r
	scheduled.Trigger = "schedule"
	scheduled.ScheduleRevision = schedule.Revision
	if claimed, err := s.ClaimFeishuReport(ctx, scheduled, now.Add(time.Minute)); err != nil || claimed {
		t.Fatal("schedule regenerated existing manual report", claimed, err)
	}
	after, _ := s.LoadFeishuSchedule(ctx)
	if after.NextWindowEnd == nil || *after.NextWindowEnd != FeishuTime(now.Add(12*time.Hour)) {
		t.Fatal("existing manual report left schedule stuck", after)
	}
	if _, _, err := s.ClaimFeishuDelivery(ctx, r.ID, r.ContentHash, b, now.Add(12*time.Hour), false); !errors.Is(err, ErrFeishuExpired) {
		t.Fatal("send allowed at next half-open boundary", err)
	}
	if _, _, err := s.ClaimFeishuDelivery(ctx, r.ID, r.ContentHash, b, now.Add(-time.Nanosecond), false); !errors.Is(err, ErrFeishuExpired) {
		t.Fatal("send allowed before completed window", err)
	}
}

func TestFeishuNestedEvidenceRejectsModelDraftForeignSourcesAndSecretReflection(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	config, b := feishuTestBinding(t, s)
	now := feishuTestTime(t, "2026-11-17T08:00:01+08:00")
	first := aiTestConversation(t, s, "qq", "feishu-evidence-account", "first")
	_ = aiTestConversation(t, s, "qq", "feishu-evidence-account", "foreign")
	body := "请确认明天会议 <at user_id=\"all\">@everyone</at> & 议程"
	outgoing := importMessage("outgoing", "first", "请替我确认（本人发出）", "2026-11-16T22:00:00+08:00")
	outgoing.Direction = "outgoing"
	mustImport(t, s, ImportRequest{Platform: "qq", AccountID: "feishu-evidence-account", Messages: []ImportMessage{
		importMessage("task", "first", body, "2026-11-16T21:00:00+08:00"),
		importMessage("chatter", "first", "普通聊天内容", "2026-11-16T23:00:00+08:00"),
		importMessage("foreign-task", "foreign", "请确认另一个未授权来源", "2026-11-16T21:30:00+08:00"),
		outgoing,
	}})
	r := feishuTestReport(t, b, now, FeishuSourceRef{ConversationID: first, SettingsRevision: 0})
	if claimed, err := s.ClaimFeishuReport(ctx, r, now); err != nil || !claimed {
		t.Fatal("claim", claimed, err)
	}
	messages, err := s.LoadFeishuMessages(ctx, r.WindowStart, r.WindowEnd, 501)
	if err != nil {
		t.Fatal(err)
	}
	byBody := map[string]FeishuMessage{}
	for _, m := range messages {
		byBody[m.Message.Body] = m
	}
	evidence := byBody[body]
	if evidence.Message.ID == "" {
		t.Fatal("synthetic evidence missing")
	}
	quote, _ := FeishuQuote(body, 100)
	excerpt, _ := FeishuQuote(body, 120)
	candidate := func() FeishuReport {
		copy := r
		copy.State = "succeeded"
		copy.Summary = "合成摘要"
		copy.Todos = []FeishuTodo{{Text: "待人工核对：" + quote, EvidenceIDs: []string{evidence.Message.ID}, NeedsReview: true}}
		copy.Excerpts = []FeishuExcerpt{{MessageID: evidence.Message.ID, ConversationID: first, Text: excerpt, OccurredAt: evidence.Message.OccurredAt}}
		copy.Text = "合成报告\n" + quote
		generated := FeishuTime(now.Add(time.Second))
		copy.GeneratedAt = &generated
		return copy
	}
	for _, tc := range []struct {
		name   string
		mutate func(*FeishuReport)
	}{
		{"model-authored-draft", func(v *FeishuReport) { v.Todos[0].Text = "模型编造的无证据待办" }},
		{"unchecked-task", func(v *FeishuReport) { v.Todos[0].NeedsReview = false }},
		{"missing-evidence", func(v *FeishuReport) { v.Todos[0].EvidenceIDs = nil }},
		{"unclaimed-source", func(v *FeishuReport) {
			v.Todos[0].EvidenceIDs[0] = byBody["请确认另一个未授权来源"].Message.ID
		}},
		{"outgoing-is-not-task", func(v *FeishuReport) {
			v.Todos[0].EvidenceIDs[0] = byBody[outgoing.Body].Message.ID
			v.Todos[0].Text = "待人工核对：" + outgoing.Body
		}},
		{"chatter-is-not-task", func(v *FeishuReport) {
			v.Todos[0].EvidenceIDs[0] = byBody["普通聊天内容"].Message.ID
			v.Todos[0].Text = "待人工核对：普通聊天内容"
		}},
		{"duplicate-task-evidence", func(v *FeishuReport) { v.Todos = append(v.Todos, v.Todos[0]) }},
		{"fabricated-excerpt", func(v *FeishuReport) { v.Excerpts[0].Text += "（不存在的原文）" }},
		{"wrong-excerpt-time", func(v *FeishuReport) { v.Excerpts[0].OccurredAt = r.WindowEnd }},
		{"duplicate-excerpt", func(v *FeishuReport) { v.Excerpts = append(v.Excerpts, v.Excerpts[0]) }},
		{"secret-in-summary", func(v *FeishuReport) { v.Summary = config.AppSecret }},
		{"recipient-in-payload", func(v *FeishuReport) { v.Text += config.RecipientOpenID }},
		{"noncanonical-terminal-time", func(v *FeishuReport) { raw := "2026-11-17T08:00:02+08:00"; v.GeneratedAt = &raw }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := candidate()
			tc.mutate(&bad)
			bad.ContentHash = FeishuContentHash(bad)
			if err := s.CompleteFeishuReport(ctx, bad); !errors.Is(err, ErrInvalidInput) {
				t.Fatal("invalid evidence/payload accepted", err)
			}
		})
	}
	valid := candidate()
	valid.ContentHash = FeishuContentHash(valid)
	if err := s.CompleteFeishuReport(ctx, valid); err != nil {
		t.Fatal("valid deterministic evidence rejected", err)
	}
	loaded, err := s.FeishuReportByID(ctx, r.ID)
	if err != nil || loaded == nil || len(loaded.Todos) != 1 || len(loaded.Excerpts) != 1 || !loaded.Todos[0].NeedsReview {
		t.Fatal("valid evidence not preserved", err)
	}
	if strings.ContainsAny(loaded.Excerpts[0].Text, "<>@&") {
		t.Fatal("active markup/mention characters survived deterministic quoting")
	}
}
