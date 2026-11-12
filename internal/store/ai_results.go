package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

func newAIID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", ErrUnavailable
	}
	encoded := hex.EncodeToString(random[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}

func aiText(value string, limit int, required bool) bool {
	return utf8.ValidString(value) && len(value) <= limit && !strings.ContainsRune(value, 0) && (!required || strings.TrimSpace(value) != "")
}

func normalizeAIResult(result AIResult, context AIContext) (AIResult, error) {
	result.MemoryCandidates = append([]AICandidate(nil), result.MemoryCandidates...)
	if result.ConfigID != "" {
		decoded, err := hex.DecodeString(result.ConfigID)
		if err != nil || len(result.ConfigID) != 64 || len(decoded) != 32 {
			return AIResult{}, ErrInvalidInput
		}
	}
	switch result.Intent {
	case "greeting", "information_request", "scheduling", "commitment", "payment", "emotional_conflict", "other":
	default:
		return AIResult{}, ErrInvalidInput
	}
	if !aiText(result.Summary, 3000, true) || !aiText(result.Draft, 6000, true) || !validLabel(result.Model, true) || len(result.RiskFlags) > 16 || len(result.MemoryCandidates) > 5 || result.PromptTokens < 0 || result.PromptTokens > 10000000 || result.CompletionTokens < 0 || result.CompletionTokens > 10000000 {
		return AIResult{}, ErrInvalidInput
	}
	known := map[string]bool{}
	for _, message := range context.Messages {
		known[message.ID] = true
	}
	validEvidence := func(ids []string) bool {
		if len(ids) < 1 || len(ids) > 20 {
			return false
		}
		seen := map[string]bool{}
		for _, id := range ids {
			if !known[id] || seen[id] {
				return false
			}
			seen[id] = true
		}
		return true
	}
	if !validEvidence(result.EvidenceIDs) {
		return AIResult{}, ErrInvalidInput
	}
	seenFlags := map[string]bool{}
	for _, flag := range result.RiskFlags {
		if !validLabel(flag, true) || len(flag) > 64 || seenFlags[flag] {
			return AIResult{}, ErrInvalidInput
		}
		seenFlags[flag] = true
	}
	for i, candidate := range result.MemoryCandidates {
		if !aiText(candidate.Content, 1000, true) || !validEvidence(candidate.EvidenceIDs) {
			return AIResult{}, ErrInvalidInput
		}
		// Canonical evidence ordering gives repeated proposals a stable identity.
		result.MemoryCandidates[i].EvidenceIDs = append([]string(nil), candidate.EvidenceIDs...)
		sort.Strings(result.MemoryCandidates[i].EvidenceIDs)
	}
	if result.RiskFlags == nil {
		result.RiskFlags = []string{}
	}
	if result.MemoryCandidates == nil {
		result.MemoryCandidates = []AICandidate{}
	}
	return result, nil
}

func (s *Store) SaveAIAnalysis(conversationID, expectedHash string, result AIResult) (AIAnalysis, error) {
	if len(expectedHash) != 64 {
		return AIAnalysis{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return AIAnalysis{}, ErrUnavailable
	}
	defer tx.Rollback()
	context, err := aiContext(tx, conversationID)
	if err != nil {
		return AIAnalysis{}, err
	}
	if context.ContextHash != expectedHash || context.Settings.Mode == "paused" {
		return AIAnalysis{}, ErrStaleContext
	}
	result, err = normalizeAIResult(result, context)
	if err != nil {
		return AIAnalysis{}, err
	}
	id, err := newAIID()
	if err != nil {
		return AIAnalysis{}, err
	}
	now := time.Now().UTC().Format(storedTimeLayout)
	encoded, err := json.Marshal(result)
	if err != nil {
		return AIAnalysis{}, ErrInvalidInput
	}
	conversation := context.Conversation
	if _, err = tx.Exec(`INSERT INTO ai_analyses(id,workspace_id,connection_id,contact_id,conversation_id,context_hash,result_json,draft_body,created_at,updated_at)
 VALUES(?,?,?,?,?,?,?,?,?,?)`, id, workspaceID, conversation.ConnectionID, conversation.ContactID, conversationID, expectedHash, string(encoded), result.Draft, now, now); err != nil {
		return AIAnalysis{}, ErrUnavailable
	}
	for _, messageID := range result.EvidenceIDs {
		if _, err = tx.Exec(`INSERT INTO ai_analysis_evidence(analysis_id,message_id,workspace_id,conversation_id)VALUES(?,?,?,?)`, id, messageID, workspaceID, conversationID); err != nil {
			return AIAnalysis{}, ErrUnavailable
		}
	}
	for _, candidate := range result.MemoryCandidates {
		memoryID := stableID("ai-memory", conversationID, candidate.Content, strings.Join(candidate.EvidenceIDs, "\x00"))
		if _, err = tx.Exec(`INSERT INTO ai_memories(id,workspace_id,connection_id,contact_id,conversation_id,content,verification_state,created_at,updated_at)
 VALUES(?,?,?,?,?,?,'unverified',?,?) ON CONFLICT(id) DO NOTHING`, memoryID, workspaceID, conversation.ConnectionID, conversation.ContactID, conversationID, candidate.Content, now, now); err != nil {
			return AIAnalysis{}, ErrUnavailable
		}
		for _, messageID := range candidate.EvidenceIDs {
			if _, err = tx.Exec(`INSERT INTO ai_memory_evidence(memory_id,message_id,workspace_id,conversation_id)VALUES(?,?,?,?) ON CONFLICT(memory_id,message_id)DO NOTHING`, memoryID, messageID, workspaceID, conversationID); err != nil {
				return AIAnalysis{}, ErrUnavailable
			}
		}
	}
	if tx.Commit() != nil {
		return AIAnalysis{}, ErrUnavailable
	}
	return AIAnalysis{ID: id, ConversationID: conversationID, ContextHash: expectedHash, AIResult: result, CreatedAt: now, UpdatedAt: now, Stale: false}, nil
}

func (s *Store) LastAIAnalysis(conversationID string) (*AIAnalysis, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, ErrUnavailable
	}
	defer tx.Rollback()
	context, err := aiContext(tx, conversationID)
	if err != nil {
		return nil, err
	}
	var result AIAnalysis
	var encoded, draft string
	err = tx.QueryRow(`SELECT id,context_hash,result_json,draft_body,draft_edited,created_at,updated_at FROM ai_analyses
 WHERE workspace_id=? AND connection_id=? AND contact_id=? AND conversation_id=? ORDER BY seq DESC LIMIT 1`, workspaceID, context.Conversation.ConnectionID, context.Conversation.ContactID, conversationID).Scan(&result.ID, &result.ContextHash, &encoded, &draft, &result.DraftEdited, &result.CreatedAt, &result.UpdatedAt)
	if err == sql.ErrNoRows {
		if tx.Commit() != nil {
			return nil, ErrUnavailable
		}
		return nil, nil
	}
	if err != nil {
		return nil, ErrUnavailable
	}
	if json.Unmarshal([]byte(encoded), &result.AIResult) != nil {
		return nil, ErrUnavailable
	}
	result.ConversationID = conversationID
	result.Draft = draft
	result.Stale = context.ContextHash != result.ContextHash
	if tx.Commit() != nil {
		return nil, ErrUnavailable
	}
	return &result, nil
}

func (s *Store) UpdateAIDraft(conversationID, analysisID, body, expectedConfigID string) error {
	if !validResourceID(analysisID) || !aiText(body, 6000, true) {
		return ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return ErrUnavailable
	}
	defer tx.Rollback()
	context, err := aiContext(tx, conversationID)
	if err != nil {
		return err
	}
	var expectedHash, encodedResult string
	err = tx.QueryRow(`SELECT context_hash,result_json FROM ai_analyses WHERE workspace_id=? AND conversation_id=? AND id=?`, workspaceID, conversationID, analysisID).Scan(&expectedHash, &encodedResult)
	if err == sql.ErrNoRows {
		return ErrNotFound
	}
	if err != nil {
		return ErrUnavailable
	}
	var savedResult AIResult
	if json.Unmarshal([]byte(encodedResult), &savedResult) != nil {
		return ErrUnavailable
	}
	if expectedHash != context.ContextHash || context.Settings.Mode == "paused" || savedResult.ConfigID != expectedConfigID {
		return ErrStaleContext
	}
	if _, err = tx.Exec(`UPDATE ai_analyses SET draft_body=?,draft_edited=1,updated_at=? WHERE workspace_id=? AND conversation_id=? AND id=?`, body, time.Now().UTC().Format(storedTimeLayout), workspaceID, conversationID, analysisID); err != nil {
		return ErrUnavailable
	}
	if tx.Commit() != nil {
		return ErrUnavailable
	}
	return nil
}

func (s *Store) ListAIMemories(conversationID string) ([]AIMemory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, ErrUnavailable
	}
	defer tx.Rollback()
	conversation, err := aiConversation(tx, conversationID)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(`SELECT id,content,verification_state,created_at,updated_at FROM ai_memories
 WHERE workspace_id=? AND connection_id=? AND contact_id=? AND conversation_id=? ORDER BY updated_at DESC,id DESC LIMIT 200`, workspaceID, conversation.ConnectionID, conversation.ContactID, conversationID)
	if err != nil {
		return nil, ErrUnavailable
	}
	items := []AIMemory{}
	for rows.Next() {
		var item AIMemory
		item.ConversationID = conversationID
		if rows.Scan(&item.ID, &item.Content, &item.VerificationState, &item.CreatedAt, &item.UpdatedAt) != nil {
			_ = rows.Close()
			return nil, ErrUnavailable
		}
		items = append(items, item)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, ErrUnavailable
	}
	for i := range items {
		items[i].EvidenceIDs, err = memoryEvidence(tx, items[i].ID, conversationID)
		if err != nil {
			return nil, err
		}
	}
	if tx.Commit() != nil {
		return nil, ErrUnavailable
	}
	return items, nil
}

func (s *Store) UpdateAIMemory(conversationID, memoryID, state string) error {
	if !validResourceID(memoryID) || state != "confirmed" && state != "unverified" {
		return ErrInvalidInput
	}
	return s.changeAIMemory(conversationID, memoryID, state, false)
}

func (s *Store) DeleteAIMemory(conversationID, memoryID string) error {
	if !validResourceID(memoryID) {
		return ErrInvalidInput
	}
	return s.changeAIMemory(conversationID, memoryID, "", true)
}

func (s *Store) changeAIMemory(conversationID, memoryID, state string, remove bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return ErrUnavailable
	}
	defer tx.Rollback()
	conversation, err := aiConversation(tx, conversationID)
	if err != nil {
		return err
	}
	var currentState string
	err = tx.QueryRow(`SELECT verification_state FROM ai_memories WHERE workspace_id=? AND connection_id=? AND contact_id=? AND conversation_id=? AND id=?`, workspaceID, conversation.ConnectionID, conversation.ContactID, conversationID, memoryID).Scan(&currentState)
	if err == sql.ErrNoRows {
		return ErrNotFound
	}
	if err != nil {
		return ErrUnavailable
	}
	if !remove {
		evidence, err := memoryEvidence(tx, memoryID, conversationID)
		if err != nil {
			return err
		}
		if len(evidence) == 0 {
			return ErrInvalidInput
		}
	}
	if remove {
		_, err = tx.Exec(`DELETE FROM ai_memories WHERE workspace_id=? AND conversation_id=? AND id=?`, workspaceID, conversationID, memoryID)
	} else if currentState != state {
		_, err = tx.Exec(`UPDATE ai_memories SET verification_state=?,updated_at=? WHERE workspace_id=? AND conversation_id=? AND id=?`, state, time.Now().UTC().Format(storedTimeLayout), workspaceID, conversationID, memoryID)
	}
	if err != nil {
		return ErrUnavailable
	}
	if remove || currentState != state {
		settings, _, err := aiSettings(tx, conversationID)
		if err != nil {
			return err
		}
		if err = bumpAIPreferences(tx, conversation, settings.Mode, settings.Tone); err != nil {
			return err
		}
	}
	if tx.Commit() != nil {
		return ErrUnavailable
	}
	return nil
}
