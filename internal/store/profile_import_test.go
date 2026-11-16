package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func analysisImportRequest(count int) ImportRequest {
	req := ImportRequest{Platform: "qq", AccountID: "import-analysis-account", AccountName: "合成账号", AIOrganize: &ImportAIConsent{Confirmed: true, ModelConfigID: profileTestConfig}}
	for i := 0; i < count; i++ {
		req.Messages = append(req.Messages, importMessage(fmt.Sprintf("analysis-%03d", i), "analysis-peer", fmt.Sprintf("合成历史第%d条", i), fmt.Sprintf("2026-01-01T00:%02d:%02dZ", i/60, i%60)))
	}
	return req
}

func TestProfileImportAtomicallyQueuesAndDrainsEntireHistoryAcrossReopen(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "analysis.db")
	s, err := Open(dbPath, testKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	req := analysisImportRequest(100)
	result, err := s.ImportWithProfileAnalysis(ctx, req, profileTestConfig)
	if err != nil || result.Inserted != 100 || result.AIQueued != 1 || len(result.ConversationIDs) != 1 {
		t.Fatal("atomic import", result, err)
	}
	id := result.ConversationIDs[0]
	profile := mustProfile(t, s, id)
	if !profile.AutoOrganize || profile.ModelConfigID != profileTestConfig || profile.PublicSearchEnabled {
		t.Fatal("incorrect authorization", profile)
	}
	first, err := s.ClaimProfileJob(ctx, profileTestConfig, time.Minute)
	if err != nil || first == nil {
		t.Fatal("claim import job", err)
	}
	input, err := s.LoadProfileJobInput(ctx, *first)
	if err != nil || len(input.Messages) != 20 {
		t.Fatal("first batch", err)
	}
	seen := map[string]bool{}
	for _, m := range input.Messages {
		seen[m.ID] = true
	}
	// A caller cannot advance past the exact message batch actually loaded.
	output := profileTestResult(input)
	output.ProcessedThroughSeq = first.MaxSeq
	if err := s.CompleteProfileJob(ctx, *first, output); err != nil {
		t.Fatal(err)
	}
	if p := mustProfile(t, s, id); p.ProcessedSeq != input.ProcessedThroughSeq || p.ProcessedSeq >= p.MaxSeq {
		t.Fatal("cursor advanced beyond batch", p.ProcessedSeq, p.MaxSeq)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dbPath, testKey)
	if err != nil {
		t.Fatal("reopen", err)
	}
	for len(seen) < 100 {
		if _, err := s.ScheduleProfileJobs(ctx, profileTestConfig); err != nil {
			t.Fatal(err)
		}
		job, err := s.ClaimProfileJob(ctx, profileTestConfig, time.Minute)
		if err != nil || job == nil {
			t.Fatal("resume claim", err)
		}
		in, err := s.LoadProfileJobInput(ctx, *job)
		if err != nil || len(in.Messages) < 1 || len(in.Messages) > 20 {
			t.Fatal("resume input", err)
		}
		for _, m := range in.Messages {
			if seen[m.ID] {
				t.Fatal("processed message repeated")
			}
			seen[m.ID] = true
		}
		if err := s.CompleteProfileJob(ctx, *job, profileTestResult(in)); err != nil {
			t.Fatal(err)
		}
	}
	profile = mustProfile(t, s, id)
	if profile.ProcessedSeq != profile.MaxSeq || len(profile.Facts) != 5 || profile.Truncated {
		t.Fatal("full history not accumulated", profile.ProcessedSeq, profile.MaxSeq, len(profile.Facts))
	}
	repeated, err := s.ImportWithProfileAnalysis(ctx, req, profileTestConfig)
	if err != nil || repeated.Inserted != 0 || repeated.Duplicates != 100 || repeated.AIQueued != 0 {
		t.Fatal("duplicate import resubmitted analysis", repeated, err)
	}
}

func TestProfileImportAuthorizationFailureRollsBackMessagesAndJobs(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	req := analysisImportRequest(2)
	baseline := req
	baseline.AIOrganize = nil
	result := mustImport(t, s, baseline)
	id := result.ConversationIDs[0]
	if err := s.UpdateAISettings(id, "paused", "natural"); err != nil {
		t.Fatal(err)
	}
	request := req
	request.Messages = append([]ImportMessage{importMessage("new-for-new-peer", "other-import-peer", "应整批回滚", "2026-01-01T00:05:00Z")}, req.Messages...)
	got, err := s.ImportWithProfileAnalysis(ctx, request, profileTestConfig)
	if !errors.Is(err, ErrStaleContext) || !reflect.DeepEqual(got, ImportResult{}) {
		t.Fatal("paused conversation accepted", got, err)
	}
	if len(mustConversations(t, s, Filter{}).Items) != 1 {
		t.Fatal("paused authorization left partial import")
	}
	if profile := mustProfile(t, s, id); profile.AutoOrganize || len(profile.Jobs) != 0 {
		t.Fatal("paused authorization was enabled")
	}
	if _, err := s.Import(req); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("raw consent bypassed dedicated method", err)
	}
	if _, err := s.ImportWithProfileAnalysis(ctx, req, strings.Repeat("cd", 32)); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("model mismatch accepted", err)
	}
}

func TestProfileImportOversizeSerializedMessageFailsWithoutSkipping(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	req := analysisImportRequest(1)
	// JSON escapes each '<' into six bytes, exceeding the outbound batch cap
	// despite fitting the original plaintext import body limit.
	req.Messages[0].Body = strings.Repeat("<", 5000)
	result, err := s.ImportWithProfileAnalysis(ctx, req, profileTestConfig)
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.ClaimProfileJob(ctx, profileTestConfig, time.Minute)
	if err != nil || job == nil {
		t.Fatal("claim", err)
	}
	if _, err := s.LoadProfileJobInput(ctx, *job); !errors.Is(err, ErrProfileInputTooLarge) {
		t.Fatal("oversized serialized message was silently skipped", err)
	}
	if err := s.FailProfileJob(ctx, *job, "PROFILE_INPUT_TOO_LARGE", false); err != nil {
		t.Fatal(err)
	}
	p := mustProfile(t, s, result.ConversationIDs[0])
	if p.ProcessedSeq != 0 || len(p.Jobs) != 1 || p.Jobs[0].State != "failed" || p.Jobs[0].LastErrorCode == nil || *p.Jobs[0].LastErrorCode != "PROFILE_INPUT_TOO_LARGE" {
		t.Fatal("oversize failure not visible", p.ProcessedSeq, p.Jobs)
	}
}
