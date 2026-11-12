package store

import (
	"database/sql"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/qilinbro/msg-manager/migrations"
)

func TestAIUpgradeFromV2AndPersistedAnalysisMemoryPreferencesQuota(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-v2.db")
	uriPath := filepath.ToSlash(path)
	if runtime.GOOS == "windows" {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	q := url.Values{}
	q.Set("_pragma_key", "x'"+testKey+"'")
	u.RawQuery = q.Encode()
	db, err := sql.Open("social-sqlcipher", u.String())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"0001_messages.sql", "0002_email.sql"} {
		raw, err := migrations.Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(string(raw)); err != nil {
			t.Fatal("legacy fixture migration", err)
		}
	}
	if _, err = db.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,applied_at TEXT NOT NULL);INSERT INTO schema_migrations VALUES(1,'2026-01-01T00:00:00Z'),(2,'2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	legacy := &Store{db: db}
	id := aiTestConversation(t, legacy, "qq", "legacy-account", "legacy-peer")
	if err = legacy.UpdateConversation(id, ConversationPatch{Read: boolValue(true), Pinned: boolValue(true), Archived: boolValue(true)}); err != nil {
		t.Fatal(err)
	}
	if err = legacy.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path, testKey)
	if err != nil {
		t.Fatal("v2 to v3 upgrade failed", err)
	}
	context, err := s.AIContext(id)
	if err != nil || len(context.Messages) != 2 {
		t.Fatal("old evidence missing after upgrade", err)
	}
	page := mustConversations(t, s, Filter{Archived: true, Pinned: true})
	if len(page.Items) != 1 || page.Items[0].ID != id || page.Items[0].UnreadCount != 0 {
		t.Fatal("v2 local management state lost")
	}
	var schemaVersion int
	if s.db.QueryRow(`SELECT max(version)FROM schema_migrations`).Scan(&schemaVersion) != nil || schemaVersion != latestSchemaVersion {
		t.Fatal("v3 schema not recorded")
	}
	rows, err := s.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Fatal("v3 foreign key check failed")
	}
	_ = rows.Close()
	var indexed int
	if s.db.QueryRow(`SELECT count(*) FROM messages_fts WHERE messages_fts MATCH '合成事实'`).Scan(&indexed) != nil || indexed != 1 {
		t.Fatal("v3 upgrade lost existing FTS rows")
	}
	result := aiTestResult(context)
	result.MemoryCandidates = []AICandidate{{Content: "合成记忆持久化", EvidenceIDs: []string{context.Messages[0].ID}}}
	result.ConfigID = strings.Repeat("ab", 32)
	if _, err = s.SaveAIAnalysis(id, context.ContextHash, result); err != nil {
		t.Fatal(err)
	}
	memories, err := s.ListAIMemories(id)
	if err != nil || len(memories) != 1 {
		t.Fatal("memory proposal not persisted", err)
	}
	if err = s.UpdateAIMemory(id, memories[0].ID, "confirmed"); err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateAISettings(id, "", "professional"); err != nil {
		t.Fatal(err)
	}
	context, err = s.AIContext(id)
	if err != nil {
		t.Fatal(err)
	}
	result = aiTestResult(context)
	result.ConfigID = strings.Repeat("cd", 32)
	analysis, err := s.SaveAIAnalysis(id, context.ContextHash, result)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateAIDraft(id, analysis.ID, "改配置后的旧草稿编辑", strings.Repeat("ab", 32)); err != ErrStaleContext {
		t.Fatal("old model configuration remained editable", err)
	}
	if err = s.UpdateAIDraft(id, analysis.ID, "合成编辑草稿", result.ConfigID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReserveAICall("2026-11-12", id, 50); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path, testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	after, err := s.AIContext(id)
	if err != nil || after.ContextHash != context.ContextHash || after.Settings.Tone != "professional" || len(after.Memories) != 1 || after.Memories[0].VerificationState != "confirmed" {
		t.Fatal("AI context not stable after reopening", err)
	}
	latest, err := s.LastAIAnalysis(id)
	if err != nil || latest == nil || latest.ID != analysis.ID || latest.Draft != "合成编辑草稿" || !latest.DraftEdited || latest.Stale || latest.ConfigID != result.ConfigID {
		t.Fatal("analysis/config/draft lost on reopening", err)
	}
	count, err := s.CountAICalls("2026-11-12")
	if err != nil || count != 1 {
		t.Fatal("quota lost on reopening", err)
	}
}

func TestAIConfirmedMemoryByteBudgetAndConfigValidation(t *testing.T) {
	s := testStore(t)
	id := aiTestConversation(t, s, "qq", "self", "friend")
	for i := 0; i < 3; i++ {
		context, _ := s.AIContext(id)
		result := aiTestResult(context)
		for j := 0; j < 5; j++ {
			result.MemoryCandidates = append(result.MemoryCandidates, AICandidate{Content: strings.Repeat(string(rune('a'+i*5+j)), 900), EvidenceIDs: []string{context.Messages[0].ID}})
		}
		if _, err := s.SaveAIAnalysis(id, context.ContextHash, result); err != nil {
			t.Fatal(err)
		}
	}
	memories, err := s.ListAIMemories(id)
	if err != nil || len(memories) != 15 {
		t.Fatal("candidate collection differs", err)
	}
	for _, memory := range memories {
		if err = s.UpdateAIMemory(id, memory.ID, "confirmed"); err != nil {
			t.Fatal(err)
		}
	}
	context, err := s.AIContext(id)
	if err != nil {
		t.Fatal(err)
	}
	bytes := 0
	for _, memory := range context.Memories {
		bytes += len(memory.Content)
		if memory.VerificationState != "confirmed" {
			t.Fatal("unconfirmed memory returned")
		}
	}
	if len(context.Memories) != 4 || bytes > aiMemoryBudget || !context.Truncated {
		t.Fatal("confirmed memory byte budget not applied")
	}
	for _, configID := range []string{"secret-endpoint", strings.Repeat("x", 64), strings.Repeat("ab", 31)} {
		result := aiTestResult(context)
		result.ConfigID = configID
		if _, err = s.SaveAIAnalysis(id, context.ContextHash, result); err != ErrInvalidInput {
			t.Fatal("invalid config fingerprint accepted", err)
		}
	}
}
