package store

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func aiTestConversation(t *testing.T, s *Store, platform, account, peer string) string {
	t.Helper()
	mustImport(t, s, ImportRequest{Platform: platform, AccountID: account, Messages: []ImportMessage{importMessage("evidence-one", peer, "合成事实：周五交付原型", "2026-01-01T00:00:00Z"), importMessage("evidence-two", peer, "合成问题：准备好了吗？", "2026-01-01T00:01:00Z")}})
	page := mustConversations(t, s, Filter{Platform: platform})
	for _, item := range page.Items {
		if item.AccountID == account && item.PeerID == peer {
			return item.ID
		}
	}
	t.Fatal("test conversation not found")
	return ""
}

func aiTestResult(context AIContext) AIResult {
	ids := []string{context.Messages[len(context.Messages)-1].ID}
	return AIResult{Intent: "information_request", Summary: "合成对话摘要", Draft: "我先核对进度，再回复你。", Model: "synthetic-model", RiskFlags: []string{}, EvidenceIDs: ids, MemoryCandidates: []AICandidate{}, PromptTokens: 123, CompletionTokens: 45}
}

func TestAIContextScopesBudgetsWatermarksAndReadState(t *testing.T) {
	s := testStore(t)
	id := aiTestConversation(t, s, "qq", "same-account", "same-peer")
	otherAccount := aiTestConversation(t, s, "qq", "other-account", "same-peer")
	otherPlatform := aiTestConversation(t, s, "weixin", "same-account", "same-peer")
	foreign, err := s.AIContext(otherAccount)
	if err != nil {
		t.Fatal(err)
	}
	foreignIDs := map[string]bool{}
	for _, message := range foreign.Messages {
		foreignIDs[message.ID] = true
	}
	foreign, err = s.AIContext(otherPlatform)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range foreign.Messages {
		foreignIDs[message.ID] = true
	}
	batch := ImportRequest{Platform: "qq", AccountID: "same-account", Messages: []ImportMessage{}}
	for i := 0; i < 25; i++ {
		batch.Messages = append(batch.Messages, importMessage(fmt.Sprintf("bounded-%02d", i), "same-peer", strings.Repeat("a", 1000), fmt.Sprintf("2026-01-02T00:%02d:00Z", i)))
	}
	mustImport(t, s, batch)
	context, err := s.AIContext(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(context.Messages) != 20 || !context.Truncated || context.Settings.Mode != "draft_only" || context.Settings.Tone != "natural" {
		t.Fatal("context bounds/defaults differ", context)
	}
	bodyBytes := 0
	for i, message := range context.Messages {
		bodyBytes += len(message.Body)
		if foreignIDs[message.ID] {
			t.Fatal("foreign scope evidence returned")
		}
		if i > 0 && message.OccurredAt < context.Messages[i-1].OccurredAt {
			t.Fatal("context reading order differs")
		}
	}
	if bodyBytes > aiMessageBudget {
		t.Fatal("context exceeded byte budget")
	}
	if err = s.UpdateConversation(id, ConversationPatch{Read: boolValue(true), Pinned: boolValue(true), Archived: boolValue(true)}); err != nil {
		t.Fatal(err)
	}
	afterRead, err := s.AIContext(id)
	if err != nil || afterRead.ContextHash != context.ContextHash {
		t.Fatal("local management invalidated model input", err)
	}
	// A newly imported older message outside the selected recent window must
	// still change consent/result identity, through the all-message watermark.
	mustImport(t, s, ImportRequest{Platform: "qq", AccountID: "same-account", Messages: []ImportMessage{importMessage("older-late-arrival", "same-peer", "合成较早记录", "2025-01-01T00:00:00Z")}})
	afterImport, err := s.AIContext(id)
	if err != nil || afterImport.ContextHash == context.ContextHash {
		t.Fatal("late arrival did not invalidate context", err)
	}
	if _, err = s.SaveAIAnalysis(id, context.ContextHash, aiTestResult(context)); !errors.Is(err, ErrStaleContext) {
		t.Fatal("stale analysis accepted", err)
	}
}

func TestAIMemoryLifecycleRequiresEvidenceAndExplicitConfirmation(t *testing.T) {
	s := testStore(t)
	id := aiTestConversation(t, s, "qq", "self", "friend")
	other := aiTestConversation(t, s, "qq", "self", "other")
	context, err := s.AIContext(id)
	if err != nil {
		t.Fatal(err)
	}
	result := aiTestResult(context)
	result.MemoryCandidates = []AICandidate{{Content: "周五交付原型（合成候选）", EvidenceIDs: []string{context.Messages[0].ID}}}
	analysis, err := s.SaveAIAnalysis(id, context.ContextHash, result)
	if err != nil {
		t.Fatal(err)
	}
	memories, err := s.ListAIMemories(id)
	if err != nil || len(memories) != 1 || memories[0].VerificationState != "unverified" {
		t.Fatal("memory auto-confirmed or missing", err)
	}
	proposal, err := s.AIContext(id)
	if err != nil || proposal.ContextHash != context.ContextHash || len(proposal.Memories) != 0 {
		t.Fatal("unverified memory entered model context", err)
	}
	latest, err := s.LastAIAnalysis(id)
	if err != nil || latest == nil || latest.ID != analysis.ID || latest.Stale {
		t.Fatal("new analysis immediately stale", err)
	}
	if err = s.UpdateAIMemory(other, memories[0].ID, "confirmed"); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-conversation memory changed", err)
	}
	if err = s.UpdateAIMemory(id, memories[0].ID, "confirmed"); err != nil {
		t.Fatal(err)
	}
	confirmed, err := s.AIContext(id)
	if err != nil || len(confirmed.Memories) != 1 || confirmed.ContextHash == context.ContextHash {
		t.Fatal("explicit confirmation not reflected", err)
	}
	latest, err = s.LastAIAnalysis(id)
	if err != nil || !latest.Stale {
		t.Fatal("memory confirmation did not stale old analysis", err)
	}
	foreign, err := s.AIContext(other)
	if err != nil || len(foreign.Memories) != 0 {
		t.Fatal("confirmed memory crossed conversation", err)
	}
	if err = s.DeleteAIMemory(id, memories[0].ID); err != nil {
		t.Fatal(err)
	}
	afterDelete, err := s.AIContext(id)
	if err != nil || len(afterDelete.Memories) != 0 || afterDelete.ContextHash == confirmed.ContextHash {
		t.Fatal("deleted memory still returned", err)
	}
	memories, err = s.ListAIMemories(id)
	if err != nil || len(memories) != 0 {
		t.Fatal("deleted memory remains in list", err)
	}
}

func TestAIResultRejectsForeignReferencesAndRollsBack(t *testing.T) {
	s := testStore(t)
	id := aiTestConversation(t, s, "qq", "self", "friend")
	other := aiTestConversation(t, s, "qq", "self", "other")
	context, _ := s.AIContext(id)
	foreign, _ := s.AIContext(other)
	for _, mutate := range []func(*AIResult){
		func(r *AIResult) { r.EvidenceIDs = []string{foreign.Messages[0].ID} },
		func(r *AIResult) {
			r.MemoryCandidates = []AICandidate{{Content: "跨人候选", EvidenceIDs: []string{foreign.Messages[0].ID}}}
		},
		func(r *AIResult) { r.Draft = strings.Repeat("x", 6001) },
		func(r *AIResult) { r.Intent = "auto_send" },
		func(r *AIResult) {
			r.MemoryCandidates = []AICandidate{{Content: strings.Repeat("x", 1001), EvidenceIDs: []string{context.Messages[0].ID}}}
		},
	} {
		result := aiTestResult(context)
		mutate(&result)
		if _, err := s.SaveAIAnalysis(id, context.ContextHash, result); !errors.Is(err, ErrInvalidInput) {
			t.Fatal("invalid analysis persisted", err)
		}
	}
	latest, err := s.LastAIAnalysis(id)
	if err != nil || latest != nil {
		t.Fatal("failed analysis partially persisted", err)
	}
	memories, err := s.ListAIMemories(id)
	if err != nil || len(memories) != 0 {
		t.Fatal("failed analysis partially saved memories", err)
	}
	if _, err = s.AIContext("00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrNotFound) {
		t.Fatal("unknown AI conversation accepted", err)
	}
}

func TestAISettingsDraftEditingAndStaleControls(t *testing.T) {
	s := testStore(t)
	id := aiTestConversation(t, s, "qq", "self", "friend")
	other := aiTestConversation(t, s, "qq", "self", "other")
	context, _ := s.AIContext(id)
	analysis, err := s.SaveAIAnalysis(id, context.ContextHash, aiTestResult(context))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateAIDraft(other, analysis.ID, "越界草稿", ""); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign draft editable", err)
	}
	if err = s.UpdateAIDraft(id, analysis.ID, "用户编辑后的本地草稿", ""); err != nil {
		t.Fatal(err)
	}
	latest, err := s.LastAIAnalysis(id)
	if err != nil || !latest.DraftEdited || latest.Draft != "用户编辑后的本地草稿" || latest.Stale {
		t.Fatal("draft edit not persisted", err)
	}
	if err = s.UpdateAISettings(id, "", "professional"); err != nil {
		t.Fatal(err)
	}
	afterTone, _ := s.AIContext(id)
	if afterTone.ContextHash == context.ContextHash || afterTone.Settings.Tone != "professional" {
		t.Fatal("tone not bound to context")
	}
	if err = s.UpdateAIDraft(id, analysis.ID, "过期草稿编辑", ""); !errors.Is(err, ErrStaleContext) {
		t.Fatal("stale draft editable", err)
	}
	if err = s.UpdateAISettings(id, "paused", ""); err != nil {
		t.Fatal(err)
	}
	paused, _ := s.AIContext(id)
	if _, err = s.SaveAIAnalysis(id, paused.ContextHash, aiTestResult(paused)); !errors.Is(err, ErrStaleContext) {
		t.Fatal("paused conversation accepted analysis", err)
	}
	if err = s.UpdateAISettings(id, "auto_low_risk", ""); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("store enabled automatic sends", err)
	}
	if err = s.UpdateAISettings(other, "draft_only", "casual"); err != nil {
		t.Fatal(err)
	}
	settings, err := s.AISettings(id)
	if err != nil || settings.Mode != "paused" || settings.Tone != "professional" {
		t.Fatal("settings crossed scope", err)
	}
}

func TestAIQuotaReservationIsAtomicPersistentAndPerDay(t *testing.T) {
	s := testStore(t)
	id := aiTestConversation(t, s, "qq", "self", "friend")
	const day = "2026-11-12"
	const quota = 5
	var wait sync.WaitGroup
	var mu sync.Mutex
	accepted, denied := 0, 0
	for i := 0; i < 20; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := s.ReserveAICall(day, id, quota)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				accepted++
			} else if errors.Is(err, ErrQuotaExceeded) {
				denied++
			} else {
				t.Errorf("quota reservation: %v", err)
			}
		}()
	}
	wait.Wait()
	if accepted != quota || denied != 20-quota {
		t.Fatal("quota raced", accepted, denied)
	}
	count, err := s.CountAICalls(day)
	if err != nil || count != quota {
		t.Fatal("reserved attempts not counted", err)
	}
	if _, err = s.ReserveAICall("2026-11-13", id, quota); err != nil {
		t.Fatal("day quota not independent", err)
	}
	if _, err = s.ReserveAICall("invalid-day", id, quota); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("invalid day accepted", err)
	}
	if _, err = s.ReserveAICall(day, "00000000-0000-0000-0000-000000000000", quota); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign reservation accepted", err)
	}
}
