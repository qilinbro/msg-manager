package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const profileTestConfig = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
const profileOtherConfig = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

func profilePointer[T any](v T) *T { return &v }
func mustProfile(t *testing.T, s *Store, id string) Profile {
	t.Helper()
	p, e := s.GetProfile(context.Background(), id)
	if e != nil {
		t.Fatal("profile read", e)
	}
	return p
}
func patchProfile(t *testing.T, s *Store, id string, p ProfilePatch) Profile {
	t.Helper()
	out, e := s.UpdateProfile(context.Background(), id, p)
	if e != nil {
		t.Fatal("profile patch", e)
	}
	return out
}
func enqueueAndClaim(t *testing.T, s *Store, id, kind string) (ProfileJob, ProfileJobInput) {
	t.Helper()
	p := mustProfile(t, s, id)
	_, e := s.EnqueueProfileJob(context.Background(), id, kind, profileTestConfig, p.Revision)
	if e != nil {
		t.Fatal("profile enqueue", e)
	}
	j, e := s.ClaimProfileJob(context.Background(), profileTestConfig, 90*time.Second)
	if e != nil || j == nil {
		t.Fatal("profile claim", e)
	}
	input, e := s.LoadProfileJobInput(context.Background(), *j)
	if e != nil {
		t.Fatal("profile job input", e)
	}
	return *j, input
}
func profileTestResult(input ProfileJobInput) ProfileJobResult {
	return ProfileJobResult{Category: "client", Tags: []string{"项目"}, Summary: "合成联系人项目摘要", EvidenceIDs: []string{input.Messages[0].ID}, Facts: []ProfileFactInput{{Field: "project", Value: "合成原型项目", MessageIDs: []string{input.Messages[0].ID}}}}
}

func TestProfileDefaultsIsolationCASCategoryAndCursor(t *testing.T) {
	s := testStore(t)
	id := aiTestConversation(t, s, "qq", "profile-account", "same-peer")
	other := aiTestConversation(t, s, "qq", "other-account", "same-peer")
	p := mustProfile(t, s, id)
	if p.AutoOrganize || p.PublicSearchEnabled || p.AIPaused || p.Revision != 0 || p.Category != "unclassified" || p.Tags == nil || p.Facts == nil || p.Sources == nil || p.Jobs == nil || p.UpdatedAt != nil || !strings.Contains(p.DocumentMarkdown, "已确认资料") {
		t.Fatal("unsafe profile defaults")
	}
	p = patchProfile(t, s, id, ProfilePatch{ExpectedRevision: profilePointer(int64(0)), PublicName: profilePointer("合成公开姓名"), CategoryOverride: profilePointer("friend"), AutoOrganize: profilePointer(true), ModelConfigID: profilePointer(profileTestConfig)})
	if p.Revision != 1 || p.Category != "friend" || !p.AutoOrganize || mustProfile(t, s, other).PublicName != "" {
		t.Fatal("profile scope/settings")
	}
	if _, e := s.UpdateProfile(context.Background(), id, ProfilePatch{ExpectedRevision: profilePointer(int64(0)), Role: profilePointer("stale")}); !errors.Is(e, ErrStaleContext) {
		t.Fatal("stale settings accepted", e)
	}
	if _, e := s.EnqueueProfileJob(context.Background(), id, "organize", profileTestConfig, 0); !errors.Is(e, ErrStaleContext) {
		t.Fatal("enqueue skipped expected revision", e)
	}
	page, e := s.Conversations(Filter{Category: "friend"})
	if e != nil || len(page.Items) != 1 || page.Items[0].ID != id || page.Items[0].Category != "friend" {
		t.Fatal("classification filter", e)
	}
	patchProfile(t, s, other, ProfilePatch{ExpectedRevision: profilePointer(int64(0)), CategoryOverride: profilePointer("friend")})
	page, e = s.Conversations(Filter{Category: "friend", Limit: 1})
	if e != nil || page.NextCursor == nil {
		t.Fatal("category cursor fixture", e)
	}
	if _, e = s.Conversations(Filter{Category: "unclassified", Limit: 1, Cursor: *page.NextCursor}); !errors.Is(e, ErrInvalidCursor) {
		t.Fatal("category filter reused cursor", e)
	}
	if _, e = s.Conversations(Filter{Category: "private_sensitive_trait"}); !errors.Is(e, ErrInvalidInput) {
		t.Fatal("invalid category accepted")
	}
	if _, e = s.GetProfile(context.Background(), "not-an-id"); !errors.Is(e, ErrInvalidInput) {
		t.Fatal("invalid profile scope accepted")
	}
}

func TestProfileManualJobAppendedMessagesEvidenceAndConfirmedFacts(t *testing.T) {
	s := testStore(t)
	id := aiTestConversation(t, s, "qq", "profile-account", "peer")
	other := aiTestConversation(t, s, "qq", "other-account", "peer")
	j, input := enqueueAndClaim(t, s, id, "organize")
	if len(input.Messages) != 2 || input.Profile.ModelConfigID != profileTestConfig {
		t.Fatal("manual job didn't bind model")
	}
	maxBefore := j.MaxSeq
	foreign, _ := s.AIContext(other)
	bad := profileTestResult(input)
	bad.Facts[0].MessageIDs = []string{foreign.Messages[0].ID}
	if e := s.CompleteProfileJob(context.Background(), j, bad); !errors.Is(e, ErrInvalidInput) {
		t.Fatal("cross contact evidence accepted", e)
	}
	mustImport(t, s, ImportRequest{Platform: "qq", AccountID: "profile-account", Messages: []ImportMessage{importMessage("appended-evidence", "peer", "合成追加消息", "2026-01-01T00:03:00Z")}})
	if e := s.CompleteProfileJob(context.Background(), j, profileTestResult(input)); e != nil {
		t.Fatal("append invalidated captured prefix", e)
	}
	p := mustProfile(t, s, id)
	if p.ProcessedSeq != maxBefore || p.MaxSeq <= p.ProcessedSeq || p.AICategory != "client" || len(p.Facts) != 1 || p.Facts[0].State != "unverified" {
		t.Fatal("profile result/watermark")
	}
	p, err := s.SetProfileFact(context.Background(), id, p.Facts[0].ID, "confirmed", p.Revision)
	if err != nil {
		t.Fatal(err)
	}
	fact := p.Facts[0]
	if _, err = s.SetProfileFact(context.Background(), other, fact.ID, "confirmed", 0); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign fact reviewed", err)
	}
	j, input = enqueueAndClaim(t, s, id, "organize")
	result := profileTestResult(input)
	result.Facts[0].Value = fact.Value
	result.Facts[0].MessageIDs = fact.MessageIDs
	if err = s.CompleteProfileJob(context.Background(), j, result); err != nil {
		t.Fatal(err)
	}
	p = mustProfile(t, s, id)
	if len(p.Facts) != 1 || p.Facts[0].State != "confirmed" {
		t.Fatal("AI replaced confirmed fact")
	}
	if err = s.CompleteProfileJob(context.Background(), j, result); !errors.Is(err, ErrStaleContext) {
		t.Fatal("completed lease replay accepted", err)
	}
}

func TestProfilePublicCandidatesRequireSourcesAndDailyEnrichment(t *testing.T) {
	s := testStore(t)
	id := aiTestConversation(t, s, "weixin", "profile-account", "peer")
	patchProfile(t, s, id, ProfilePatch{ExpectedRevision: profilePointer(int64(0)), PublicName: profilePointer("合成公开人物"), PublicSearchEnabled: profilePointer(true), ModelConfigID: profilePointer(profileTestConfig)})
	j, input := enqueueAndClaim(t, s, id, "enrich")
	if len(input.Messages) != 0 {
		t.Fatal("public search input included private messages")
	}
	hash := strings.Repeat("a", 64)
	source := ProfileSource{URL: "http://example.com/work", Title: "合成公开作品", Snippet: "公开作品和职业介绍", ContentHash: hash, RetrievedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	source.ID = ProfileSourceID(id, source.URL, hash)
	result := ProfileJobResult{Summary: strings.Repeat("公开作品", 100), SourceIDs: []string{source.ID}, Sources: []ProfileSource{source}, Facts: []ProfileFactInput{{Field: "role", Value: "作品作者", SourceIDs: []string{source.ID}}}}
	bad := result
	bad.Facts = append([]ProfileFactInput{}, result.Facts...)
	bad.Facts[0].SourceIDs = []string{stableID("foreign-source")}
	if e := s.CompleteProfileJob(context.Background(), j, bad); !errors.Is(e, ErrInvalidInput) {
		t.Fatal("unknown public source accepted", e)
	}
	if e := s.CompleteProfileJob(context.Background(), j, result); e != nil {
		t.Fatal(e)
	}
	p := mustProfile(t, s, id)
	if len(p.Sources) != 1 || len(p.Facts) != 2 || p.Summary != "" || p.AICategory != "unclassified" || !p.Truncated {
		t.Fatal("public enrichment overwrote chat classification")
	}
	for _, f := range p.Facts {
		if f.Origin != "public" || f.State != "unverified" || len(f.SourceIDs) != 1 || len(f.MessageIDs) != 0 {
			t.Fatal("public source was treated as confirmed identity")
		}
	}
	if _, e := s.EnqueueProfileJob(context.Background(), id, "enrich", profileTestConfig, p.Revision); !errors.Is(e, ErrConflict) {
		t.Fatal("same-day public search not bounded", e)
	}
	other := aiTestConversation(t, s, "weixin", "other-account", "peer")
	patchProfile(t, s, other, ProfilePatch{ExpectedRevision: profilePointer(int64(0)), PublicName: profilePointer("另一公开人物"), PublicSearchEnabled: profilePointer(true), ModelConfigID: profilePointer(profileTestConfig)})
	j, _ = enqueueAndClaim(t, s, other, "enrich")
	if e := s.CompleteProfileJob(context.Background(), j, ProfileJobResult{}); e != nil {
		t.Fatal("zero search results fabricated failure", e)
	}
	if p = mustProfile(t, s, other); len(p.Facts) != 0 || len(p.Sources) != 0 {
		t.Fatal("empty search fabricated facts")
	}
}

func TestProfileLeaseRecoveryFencingAndBoundedRetries(t *testing.T) {
	s := testStore(t)
	id := aiTestConversation(t, s, "qq", "profile-account", "peer")
	j, input := enqueueAndClaim(t, s, id, "organize")
	if _, e := s.db.Exec(`UPDATE profile_jobs SET lease_until=? WHERE id=?`, time.Now().UTC().Add(-time.Minute).Format(storedTimeLayout), j.ID); e != nil {
		t.Fatal(e)
	}
	newJob, e := s.ClaimProfileJob(context.Background(), profileTestConfig, 90*time.Second)
	if e != nil || newJob == nil || newJob.Fence <= j.Fence || newJob.LeaseToken == j.LeaseToken || newJob.Attempts != 2 {
		t.Fatal("expired work did not receive fenced lease", e)
	}
	if e = s.CompleteProfileJob(context.Background(), j, profileTestResult(input)); !errors.Is(e, ErrStaleContext) {
		t.Fatal("old worker completed new lease", e)
	}
	if e = s.FailProfileJob(context.Background(), *newJob, "AI_UNAVAILABLE", true); e != nil {
		t.Fatal(e)
	}
	p := mustProfile(t, s, id)
	if p.Jobs[0].State != "queued" || p.Jobs[0].Attempts != 2 || p.Jobs[0].NextAttemptAt == nil {
		t.Fatal("failure did not back off")
	}
	if _, e = s.db.Exec(`UPDATE profile_jobs SET next_attempt_at=? WHERE id=?`, time.Now().UTC().Add(-time.Minute).Format(storedTimeLayout), j.ID); e != nil {
		t.Fatal(e)
	}
	newJob, e = s.ClaimProfileJob(context.Background(), profileTestConfig, 90*time.Second)
	if e != nil || newJob == nil || newJob.Attempts != 3 {
		t.Fatal(e)
	}
	if e = s.FailProfileJob(context.Background(), *newJob, "AI_UNAVAILABLE", true); e != nil {
		t.Fatal(e)
	}
	p = mustProfile(t, s, id)
	if p.Jobs[0].State != "failed" || p.Jobs[0].NextAttemptAt != nil {
		t.Fatal("retry limit was not terminal")
	}
	if job, e := s.ClaimProfileJob(context.Background(), profileTestConfig, 90*time.Second); e != nil || job != nil {
		t.Fatal("failed job retried unboundedly")
	}
}

func TestProfilePauseSettingsAndModelConsentAreNotBypassed(t *testing.T) {
	s := testStore(t)
	id := aiTestConversation(t, s, "qq", "profile-account", "peer")
	p := patchProfile(t, s, id, ProfilePatch{ExpectedRevision: profilePointer(int64(0)), AutoOrganize: profilePointer(true), ModelConfigID: profilePointer(profileTestConfig)})
	j, _ := enqueueAndClaim(t, s, id, "organize")
	if e := s.UpdateAISettings(id, "paused", ""); e != nil {
		t.Fatal(e)
	}
	if e := s.CheckProfileLease(context.Background(), j); !errors.Is(e, ErrStaleContext) {
		t.Fatal("legacy AI pause did not fence profile work", e)
	}
	p = mustProfile(t, s, id)
	if !p.AIPaused {
		t.Fatal("profile did not expose pause")
	}
	p = patchProfile(t, s, id, ProfilePatch{ExpectedRevision: profilePointer(p.Revision), ManualNotes: profilePointer("暂停期间允许人工编辑"), AutoOrganize: profilePointer(true), ModelConfigID: profilePointer(profileTestConfig)})
	if _, e := s.EnqueueProfileJob(context.Background(), id, "organize", profileTestConfig, p.Revision); !errors.Is(e, ErrStaleContext) {
		t.Fatal("manual profile bypassed AI pause")
	}
	if e := s.UpdateAISettings(id, "draft_only", ""); e != nil {
		t.Fatal(e)
	}
	if _, e := s.ScheduleProfileJobs(context.Background(), profileTestConfig); e != nil {
		t.Fatal(e)
	}
	p = mustProfile(t, s, id)
	if p.Jobs[0].State != "queued" {
		t.Fatal("resume did not recover cancelled work")
	}
	if _, e := s.ScheduleProfileJobs(context.Background(), profileOtherConfig); e != nil {
		t.Fatal(e)
	}
	p = mustProfile(t, s, id)
	if p.ModelConfigID != profileTestConfig || p.Jobs[0].State != "cancelled" {
		t.Fatal("model change silently migrated authorization")
	}
	if _, e := s.EnqueueProfileJob(context.Background(), id, "organize", profileOtherConfig, p.Revision); e != nil {
		t.Fatal("explicit one-time new model rejected", e)
	}
	job, e := s.ClaimProfileJob(context.Background(), profileOtherConfig, 90*time.Second)
	if e != nil || job == nil {
		t.Fatal("one-time new model not claimable", e)
	}
	if p = mustProfile(t, s, id); p.ModelConfigID != profileTestConfig {
		t.Fatal("one-time request migrated auto consent")
	}
	if _, e := s.LoadProfileJobInput(context.Background(), *job); e != nil {
		t.Fatal(e)
	}
}

func TestProfileQuotaCancellationAndVisibleBounds(t *testing.T) {
	s := testStore(t)
	id := aiTestConversation(t, s, "qq", "profile-account", "peer")
	j, _ := enqueueAndClaim(t, s, id, "organize")
	if e := s.FailProfileJob(context.Background(), j, "AI_QUOTA_EXCEEDED", true); e != nil {
		t.Fatal(e)
	}
	p := mustProfile(t, s, id)
	if p.Jobs[0].Attempts != 0 || p.Jobs[0].State != "queued" || p.Jobs[0].NextAttemptAt == nil || *p.Jobs[0].NextAttemptAt <= profileNow() {
		t.Fatal("quota exhausted retry attempts or lost next-day scheduling")
	}
	if _, e := s.ReserveAICallContext(context.Background(), profileDay(), id, 1); e != nil {
		t.Fatal(e)
	}
	if _, e := s.ReserveAICallContext(context.Background(), profileDay(), id, 1); !errors.Is(e, ErrQuotaExceeded) {
		t.Fatal("profile bypassed shared AI quota")
	}
	if _, e := s.ReserveProfileSearch(context.Background(), profileDay(), id, 1); e != nil {
		t.Fatal(e)
	}
	if _, e := s.ReserveProfileSearch(context.Background(), profileDay(), id, 1); !errors.Is(e, ErrQuotaExceeded) {
		t.Fatal("search quota not durable")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	done := make(chan error, 1)
	go func() {
		_, e := s.UpdateProfile(ctx, id, ProfilePatch{ExpectedRevision: profilePointer(p.Revision), ManualNotes: profilePointer("must-not-save")})
		done <- e
	}()
	cancel()
	select {
	case e := <-done:
		if !errors.Is(e, context.Canceled) {
			s.mu.Unlock()
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		s.mu.Unlock()
		t.Fatal("cancelled profile mutation waited for storage mutex")
	}
	s.mu.Unlock()
	if mustProfile(t, s, id).ManualNotes != "" {
		t.Fatal("cancelled settings saved later")
	}
	if _, e := s.db.Exec(`UPDATE profile_jobs SET next_attempt_at=? WHERE id=?`, time.Now().UTC().Add(-time.Minute).Format(storedTimeLayout), j.ID); e != nil {
		t.Fatal(e)
	}
	job, e := s.ClaimProfileJob(context.Background(), profileTestConfig, time.Minute)
	if e != nil || job == nil {
		t.Fatal(e)
	}
	if e = s.FailProfileJob(context.Background(), *job, "PROFILE_CANCELLED", true); e != nil {
		t.Fatal(e)
	}
	if mustProfile(t, s, id).Jobs[0].Attempts != 0 {
		t.Fatal("normal service cancellation exhausted retries")
	}
	for i := 0; i < 105; i++ {
		fact := stableID("visible-fact", fmt.Sprint(i))
		if _, e = s.db.Exec(`INSERT INTO profile_facts(id,workspace_id,conversation_id,field,value,state,origin,created_at,updated_at) VALUES(?,?,?,'project',?,'unverified','chat',?,?)`, fact, workspaceID, id, "合成候选", profileNow(), profileNow()); e != nil {
			t.Fatal(e)
		}
	}
	p = mustProfile(t, s, id)
	if len(p.Facts) != 100 || !p.Truncated || len(p.DocumentMarkdown) > 180000 || !strings.Contains(p.DocumentMarkdown, "有限消息") {
		t.Fatal("profile response silently unbounded")
	}
}

func TestProfileInputJSONBudgetAndMigrationPreserveExistingGraph(t *testing.T) {
	s := testStore(t)
	id := aiTestConversation(t, s, "qq", "profile-account", "peer")
	for i := 0; i < 25; i++ {
		mustImport(t, s, ImportRequest{Platform: "qq", AccountID: "profile-account", Messages: []ImportMessage{importMessage(fmt.Sprint("batch-", i), "peer", strings.Repeat("\"", 700), "2026-01-01T00:03:00Z")}})
	}
	j, input := enqueueAndClaim(t, s, id, "organize")
	raw, _ := json.Marshal(input.Messages)
	if !input.Truncated || len(raw) > 20000 || len(input.Messages) > 20 {
		t.Fatal("serialized context budget not enforced")
	}
	if e := s.CompleteProfileJob(context.Background(), j, profileTestResult(input)); e != nil {
		t.Fatal(e)
	}
	if !mustProfile(t, s, id).Truncated {
		t.Fatal("limited input was presented as full history")
	}
	path := filepath.Join(t.TempDir(), "legacy-v5.db")
	legacy, e := Open(path, testKey)
	if e != nil {
		t.Fatal(e)
	}
	legacyID := aiTestConversation(t, legacy, "qq", "legacy-profile", "peer")
	aiContext, _ := legacy.AIContext(legacyID)
	if _, e = legacy.SaveAIAnalysis(legacyID, aiContext.ContextHash, aiTestResult(aiContext)); e != nil {
		t.Fatal(e)
	}
	before := snapshotV3Data(t, legacy.db)
	for _, table := range []string{"profile_fact_messages", "profile_fact_sources", "profile_facts", "profile_sources", "profile_jobs", "profile_search_reservations", "contact_profiles"} {
		if _, e = legacy.db.Exec(`DROP TABLE ` + table); e != nil {
			legacy.Close()
			t.Fatal(e)
		}
	}
	if _, e = legacy.db.Exec(`DELETE FROM schema_migrations WHERE version=6`); e != nil {
		legacy.Close()
		t.Fatal(e)
	}
	if e = legacy.Close(); e != nil {
		t.Fatal(e)
	}
	upgraded, e := Open(path, testKey)
	if e != nil {
		t.Fatal(e)
	}
	defer upgraded.Close()
	if !reflect.DeepEqual(before, snapshotV3Data(t, upgraded.db)) {
		t.Fatal("profile migration changed existing AI/message graph")
	}
	p := mustProfile(t, upgraded, legacyID)
	if p.AutoOrganize || p.PublicSearchEnabled || p.Revision != 0 {
		t.Fatal("migration auto authorized AI")
	}
	var version int
	if upgraded.db.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&version) != nil || version != 6 {
		t.Fatal("profile migration missing")
	}
}

func TestProfileJobsSurviveReopenAndSerializeSameContactAcrossStores(t *testing.T) {
	path := filepath.Join(t.TempDir(), "persisted-profile-jobs.db")
	first, err := Open(path, testKey)
	if err != nil {
		t.Fatal(err)
	}
	id := aiTestConversation(t, first, "weixin", "persisted-profile", "peer")
	p := patchProfile(t, first, id, ProfilePatch{ExpectedRevision: profilePointer(int64(0)), PublicName: profilePointer("合成公开人物"), PublicSearchEnabled: profilePointer(true), ModelConfigID: profilePointer(profileTestConfig)})
	if _, err = first.EnqueueProfileJob(context.Background(), id, "organize", profileTestConfig, p.Revision); err != nil {
		first.Close()
		t.Fatal(err)
	}
	if _, err = first.EnqueueProfileJob(context.Background(), id, "enrich", profileTestConfig, p.Revision); err != nil {
		first.Close()
		t.Fatal(err)
	}
	old, err := first.ClaimProfileJob(context.Background(), profileTestConfig, 90*time.Second)
	if err != nil || old == nil || old.Kind != "organize" {
		first.Close()
		t.Fatal("initial claim", err)
	}
	oldInput, err := first.LoadProfileJobInput(context.Background(), *old)
	if err != nil {
		first.Close()
		t.Fatal(err)
	}
	if _, err = first.ReserveProfileSearch(context.Background(), profileDay(), id, 20); err != nil {
		first.Close()
		t.Fatal(err)
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	otherStore, err := Open(path, testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer otherStore.Close()
	if count, e := reopened.CountProfileSearch(context.Background(), profileDay()); e != nil || count != 1 {
		t.Fatal("search ledger lost on reopen", count, e)
	}
	if j, e := otherStore.ClaimProfileJob(context.Background(), profileTestConfig, 90*time.Second); e != nil || j != nil {
		t.Fatal("second kind ran under another store's active contact lease", e)
	}
	if _, err = reopened.db.Exec(`UPDATE profile_jobs SET lease_until=? WHERE id=?`, time.Now().UTC().Add(-time.Minute).Format(storedTimeLayout), old.ID); err != nil {
		t.Fatal(err)
	}
	next, err := otherStore.ClaimProfileJob(context.Background(), profileTestConfig, 90*time.Second)
	if err != nil || next == nil || next.Kind != "enrich" {
		t.Fatal("persisted queued enrichment not recovered", err)
	}
	if _, err = otherStore.LoadProfileJobInput(context.Background(), *next); err != nil {
		t.Fatal(err)
	}
	if err = otherStore.CompleteProfileJob(context.Background(), *next, ProfileJobResult{}); err != nil {
		t.Fatal(err)
	}
	recovered, err := reopened.ClaimProfileJob(context.Background(), profileTestConfig, 90*time.Second)
	if err != nil || recovered == nil || recovered.ID != old.ID || recovered.LeaseToken == old.LeaseToken || recovered.Fence <= old.Fence {
		t.Fatal("expired persisted work did not receive a new fence", err)
	}
	if err = reopened.CompleteProfileJob(context.Background(), *old, profileTestResult(oldInput)); !errors.Is(err, ErrStaleContext) {
		t.Fatal("old pre-restart worker completed new lease", err)
	}
	input, err := reopened.LoadProfileJobInput(context.Background(), *recovered)
	if err != nil {
		t.Fatal(err)
	}
	if err = reopened.CompleteProfileJob(context.Background(), *recovered, profileTestResult(input)); err != nil {
		t.Fatal(err)
	}
	if p = mustProfile(t, reopened, id); p.Category != "client" || len(p.Jobs) != 2 || p.Jobs[0].State != "succeeded" || p.Jobs[1].State != "succeeded" {
		t.Fatal("persisted work did not complete")
	}
}

func TestProfileSchedulingSkipsUnchangedContactsBeforeBoundedScan(t *testing.T) {
	s := testStore(t)
	id := aiTestConversation(t, s, "qq", "late-scheduler-contact", "peer")
	p := patchProfile(t, s, id, ProfilePatch{ExpectedRevision: profilePointer(int64(0)), AutoOrganize: profilePointer(true), ModelConfigID: profilePointer(profileTestConfig)})
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var connection string
	if err = tx.QueryRow(`SELECT connection_id FROM conversations WHERE id=?`, id).Scan(&connection); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	for i := 0; i < 1001; i++ {
		// These lower-sorting contacts have no messages to organize. They must
		// not occupy a bounded scheduler scan before the actual dirty contact.
		fake := fmt.Sprintf("00000000-0000-0000-0000-%012x", i)
		peer := fmt.Sprintf("unchanged-%d", i)
		if _, err = tx.Exec(`INSERT INTO contacts(id,workspace_id,connection_id,peer_id,display_name)VALUES(?,?,?,?,?)`, fake, workspaceID, connection, peer, "合成空联系人"); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if _, err = tx.Exec(`INSERT INTO conversations(id,workspace_id,connection_id,contact_id,peer_id,title)VALUES(?,?,?,?,?,?)`, fake, workspaceID, connection, fake, peer, "合成空联系人"); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if _, err = tx.Exec(`INSERT INTO contact_profiles(conversation_id,workspace_id,connection_id,contact_id,auto_organize,model_config_id,updated_at)VALUES(?,?,?,?,1,?,?)`, fake, workspaceID, connection, fake, profileTestConfig, profileNow()); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if n, e := s.ScheduleProfileJobs(context.Background(), profileTestConfig); e != nil || n != 1 {
		t.Fatal("unchanged profiles starved a later dirty contact", n, e)
	}
	job, err := s.ClaimProfileJob(context.Background(), profileTestConfig, time.Minute)
	if err != nil || job == nil || job.ConversationID != id || job.SettingsRevision != p.Revision {
		t.Fatal("dirty contact was not scheduled", err)
	}
}
