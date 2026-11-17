package store

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestDigestForceNeverReplaysRunningOrTerminalReport(t *testing.T) {
	for _, state := range []string{"running", "succeeded", "failed", "unknown"} {
		t.Run(state, func(t *testing.T) {
			s := testStore(t)
			ctx := context.Background()
			const day = "2026-11-17"
			claimed, err := s.ClaimDigestRun(ctx, day, true)
			if err != nil || !claimed {
				t.Fatal("manual claim of disabled schedule", claimed, err)
			}
			report := DigestReport{ID: DigestReportID(day), Day: day, State: state, Summary: "synthetic-one-time-report", Reminders: []DigestReminder{}, EvidenceIDs: []string{}}
			if state != "running" {
				wrong := report
				wrong.ID = "wrong-report-id"
				if err := s.CompleteDigestReport(ctx, wrong); !errors.Is(err, ErrStaleContext) {
					t.Fatal("wrong ID was silently accepted", err)
				}
				if err := s.CompleteDigestReport(ctx, report); err != nil {
					t.Fatal(err)
				}
				if err := s.CompleteDigestReport(ctx, report); !errors.Is(err, ErrStaleContext) {
					t.Fatal("terminal report overwritten", err)
				}
			}
			var wg sync.WaitGroup
			var duplicate atomic.Int32
			for range 8 {
				wg.Go(func() {
					claimed, err := s.ClaimDigestRun(ctx, day, true)
					if err != nil {
						t.Error(err)
					}
					if claimed {
						duplicate.Add(1)
					}
				})
			}
			wg.Wait()
			if duplicate.Load() != 0 {
				t.Fatal("force replayed daily model work", duplicate.Load())
			}
			loaded, err := s.DigestReportForDay(ctx, day)
			if err != nil || loaded == nil || loaded.State != state || loaded.ID != report.ID {
				t.Fatal("daily report lost", loaded, err)
			}
			schedule, err := s.LoadDigestSchedule(ctx)
			if err != nil {
				t.Fatal(err)
			}
			schedule.LocalTime = "10:00"
			saved, err := s.SaveDigestSchedule(ctx, schedule, schedule.Revision)
			if err != nil || saved.LastRunDay != day {
				t.Fatal("save response invented a cleared last-run day", saved, err)
			}
			if _, err := s.ClaimDigestRun(ctx, "2026-11-33", true); !errors.Is(err, ErrInvalidInput) {
				t.Fatal("invalid date accepted", err)
			}
		})
	}
}

func TestDigestMessageWindowNormalizesTimezoneAndExcludesPaused(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	mustImport(t, s, ImportRequest{Platform: "qq", AccountID: "synthetic-digest-window", Messages: []ImportMessage{
		importMessage("before", "allowed", "outside window before", "2026-11-17T00:29:59Z"),
		importMessage("inside", "allowed", "inside normalized Shanghai window", "2026-11-17T01:10:00Z"),
		importMessage("end", "allowed", "exclusive end", "2026-11-17T02:00:00Z"),
		importMessage("paused", "paused", "paused synthetic private text", "2026-11-17T01:20:00Z"),
	}})
	var pausedID string
	for _, item := range mustConversations(t, s, Filter{Platform: "qq"}).Items {
		if item.PeerID == "paused" {
			pausedID = item.ID
		}
	}
	if pausedID == "" {
		t.Fatal("missing paused fixture")
	}
	if paused, err := s.HasPausedConversationsContext(ctx); err != nil || paused {
		t.Fatal(paused, err)
	}
	if err := s.UpdateAISettings(pausedID, "paused", "natural"); err != nil {
		t.Fatal(err)
	}
	if paused, err := s.HasPausedConversationsContext(ctx); err != nil || !paused {
		t.Fatal(paused, err)
	}
	items, err := s.LoadDigestMessages(ctx, "2026-11-17T08:30:00+08:00", "2026-11-17T10:00:00+08:00", 100)
	if err != nil || len(items) != 1 || items[0].Message.Body != "inside normalized Shanghai window" {
		t.Fatal("window leaked paused or offset-excluded messages", items, err)
	}
	if _, err := s.LoadDigestMessages(ctx, "not-a-time", "2026-11-17T10:00:00+08:00", 100); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("invalid bounds accepted", err)
	}
}

func TestEveryModelSaveRevokesDigestConsentAtomically(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	current := ModelConfig{BaseURL: "https://synthetic.invalid", Model: "model-a", APIKey: "synthetic-only", DailyLimit: 10}
	for _, change := range []string{"initial", "same", "key", "limit", "model-b", "model-a"} {
		schedule, err := s.LoadDigestSchedule(ctx)
		if err != nil {
			t.Fatal(err)
		}
		schedule.Enabled, schedule.ModelConfigID, schedule.ModelConfigRevision = true, modelConfigTarget(current), current.Revision
		schedule, err = s.SaveDigestSchedule(ctx, schedule, schedule.Revision)
		if err != nil {
			t.Fatal(err)
		}
		switch change {
		case "key":
			current.APIKey = "synthetic-updated-key"
		case "limit":
			current.DailyLimit = 15
		case "model-b":
			current.Model = "model-b"
		case "model-a":
			current.Model = "model-a"
		}
		current, err = s.SaveModelConfig(ctx, current, current.Revision)
		if err != nil {
			t.Fatal(change, err)
		}
		loaded, err := s.LoadDigestSchedule(ctx)
		if err != nil || loaded.Enabled || loaded.ModelConfigID != "" || loaded.ModelConfigRevision != 0 || loaded.Revision != schedule.Revision+1 {
			t.Fatal("model save retained consent", change, loaded, err)
		}
		if _, err := s.SaveDigestSchedule(ctx, schedule, schedule.Revision); !errors.Is(err, ErrStaleContext) {
			t.Fatal("stale schedule survived config mutation", change, err)
		}
	}
}
