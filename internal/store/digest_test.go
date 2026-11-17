package store

import (
	"context"
	"testing"
)

func TestDigestScheduleCASAndDailyReportIdempotency(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	schedule, err := s.LoadDigestSchedule(ctx)
	if err != nil || schedule.Enabled || schedule.LocalTime != "08:00" || schedule.Revision != 1 {
		t.Fatal("unexpected default digest schedule", schedule, err)
	}
	schedule, err = s.SaveDigestSchedule(ctx, DigestSchedule{Enabled: true, LocalTime: "09:30", Timezone: "Asia/Shanghai", ModelConfigID: profileTestConfig, ModelConfigRevision: 1}, schedule.Revision)
	if err != nil || !schedule.Enabled || schedule.LocalTime != "09:30" || schedule.Revision != 2 {
		t.Fatal("digest schedule save", schedule, err)
	}
	if _, err = s.SaveDigestSchedule(ctx, DigestSchedule{Enabled: false, LocalTime: "09:30", Timezone: "Asia/Shanghai"}, 1); err != ErrStaleContext {
		t.Fatal("stale digest schedule accepted", err)
	}
	claimed, err := s.ClaimDigestRun(ctx, "2026-11-16", false)
	if err != nil || !claimed {
		t.Fatal("first digest claim", claimed, err)
	}
	claimed, err = s.ClaimDigestRun(ctx, "2026-11-16", false)
	if err != nil || claimed {
		t.Fatal("duplicate digest claim", claimed, err)
	}
	report := DigestReport{ID: stableID("digest-report", "2026-11-16"), Day: "2026-11-16", State: "succeeded", Summary: "合成摘要", Reminders: []DigestReminder{{ConversationID: "conversation-one", Title: "合成会话", Priority: "high", Reason: "合成提醒", EvidenceIDs: []string{"message-one"}}}, EvidenceIDs: []string{"message-one"}, ConversationCount: 1, GeneratedAt: ptrDigestString("2026-11-16T08:00:00Z")}
	if err = s.CompleteDigestReport(ctx, report); err != nil {
		t.Fatal("complete digest", err)
	}
	loaded, err := s.LatestDigestReport(ctx)
	if err != nil || loaded == nil || loaded.Summary != report.Summary || len(loaded.Reminders) != 1 || loaded.Reminders[0].EvidenceIDs[0] != "message-one" {
		t.Fatal("load digest report", loaded, err)
	}
}

func ptrDigestString(value string) *string { return &value }
