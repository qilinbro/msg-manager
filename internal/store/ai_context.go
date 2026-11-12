package store

import (
	"database/sql"
	"encoding/json"
)

const aiMessageBudget = 20000
const aiMemoryBudget = 4000

func aiConversation(tx *sql.Tx, id string) (AIConversation, error) {
	if !validResourceID(id) {
		return AIConversation{}, ErrInvalidInput
	}
	var conversation AIConversation
	err := tx.QueryRow(`SELECT v.id,v.connection_id,v.contact_id,c.platform,v.title
 FROM conversations v JOIN connections c ON c.id=v.connection_id AND c.workspace_id=v.workspace_id
 JOIN contacts p ON p.id=v.contact_id AND p.connection_id=v.connection_id AND p.workspace_id=v.workspace_id
 WHERE v.workspace_id=? AND v.id=?`, workspaceID, id).Scan(&conversation.ID, &conversation.ConnectionID, &conversation.ContactID, &conversation.Platform, &conversation.Title)
	if err == sql.ErrNoRows {
		return AIConversation{}, ErrNotFound
	}
	if err != nil {
		return AIConversation{}, ErrUnavailable
	}
	return conversation, nil
}

func aiSettings(tx *sql.Tx, conversationID string) (AISettings, int64, error) {
	settings := AISettings{Mode: "draft_only", Tone: "natural"}
	var revision int64
	err := tx.QueryRow(`SELECT mode,tone,revision FROM ai_preferences WHERE workspace_id=? AND conversation_id=?`, workspaceID, conversationID).Scan(&settings.Mode, &settings.Tone, &revision)
	if err == sql.ErrNoRows {
		return settings, 0, nil
	}
	if err != nil {
		return AISettings{}, 0, ErrUnavailable
	}
	return settings, revision, nil
}

func memoryEvidence(tx *sql.Tx, memoryID, conversationID string) ([]string, error) {
	rows, err := tx.Query(`SELECT e.message_id FROM ai_memory_evidence e JOIN messages m
 ON m.id=e.message_id AND m.workspace_id=e.workspace_id AND m.conversation_id=e.conversation_id
 WHERE e.workspace_id=? AND e.conversation_id=? AND e.memory_id=? ORDER BY e.message_id`, workspaceID, conversationID, memoryID)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) != nil {
			return nil, ErrUnavailable
		}
		ids = append(ids, id)
	}
	if rows.Err() != nil {
		return nil, ErrUnavailable
	}
	return ids, nil
}

func aiContext(tx *sql.Tx, conversationID string) (AIContext, error) {
	conversation, err := aiConversation(tx, conversationID)
	if err != nil {
		return AIContext{}, err
	}
	settings, settingsRevision, err := aiSettings(tx, conversationID)
	if err != nil {
		return AIContext{}, err
	}
	result := AIContext{Conversation: conversation, Messages: []AIMessage{}, Memories: []AIMemory{}, Settings: settings}
	var maxSequence, totalMessages int64
	if tx.QueryRow(`SELECT coalesce(max(seq),0),count(*) FROM messages WHERE workspace_id=? AND conversation_id=?`, workspaceID, conversationID).Scan(&maxSequence, &totalMessages) != nil {
		return AIContext{}, ErrUnavailable
	}
	rows, err := tx.Query(`SELECT id,body,direction,occurred_at FROM messages WHERE workspace_id=? AND conversation_id=? ORDER BY occurred_at DESC,seq DESC LIMIT 20`, workspaceID, conversationID)
	if err != nil {
		return AIContext{}, ErrUnavailable
	}
	used := 0
	for rows.Next() {
		var message AIMessage
		if rows.Scan(&message.ID, &message.Body, &message.Direction, &message.OccurredAt) != nil {
			_ = rows.Close()
			return AIContext{}, ErrUnavailable
		}
		if used+len(message.Body) > aiMessageBudget {
			result.Truncated = true
			break
		}
		used += len(message.Body)
		result.Messages = append(result.Messages, message)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return AIContext{}, ErrUnavailable
	}
	if totalMessages > int64(len(result.Messages)) {
		result.Truncated = true
	}
	for left, right := 0, len(result.Messages)-1; left < right; left, right = left+1, right-1 {
		result.Messages[left], result.Messages[right] = result.Messages[right], result.Messages[left]
	}
	rows, err = tx.Query(`SELECT id,content,verification_state,created_at,updated_at FROM ai_memories
 WHERE workspace_id=? AND connection_id=? AND contact_id=? AND conversation_id=? AND verification_state='confirmed'
 ORDER BY updated_at DESC,id DESC LIMIT 9`, workspaceID, conversation.ConnectionID, conversation.ContactID, conversationID)
	if err != nil {
		return AIContext{}, ErrUnavailable
	}
	memories := []AIMemory{}
	used = 0
	for rows.Next() {
		var memory AIMemory
		memory.ConversationID = conversationID
		if rows.Scan(&memory.ID, &memory.Content, &memory.VerificationState, &memory.CreatedAt, &memory.UpdatedAt) != nil {
			_ = rows.Close()
			return AIContext{}, ErrUnavailable
		}
		if len(memories) >= 8 || used+len(memory.Content) > aiMemoryBudget {
			result.Truncated = true
			break
		}
		used += len(memory.Content)
		memories = append(memories, memory)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return AIContext{}, ErrUnavailable
	}
	for _, memory := range memories {
		memory.EvidenceIDs, err = memoryEvidence(tx, memory.ID, conversationID)
		if err != nil {
			return AIContext{}, err
		}
		if len(memory.EvidenceIDs) == 0 {
			return AIContext{}, ErrUnavailable
		}
		result.Memories = append(result.Memories, memory)
	}
	// Immutable message identities/content, an all-message watermark, confirmed
	// memories and manual preference revision bind consent and result writes.
	// Local read/pin/archive and newly proposed unverified memories do not alter
	// model input, so they intentionally do not change this fingerprint.
	raw, err := json.Marshal(struct {
		Conversation  AIConversation
		Messages      []AIMessage
		Memories      []AIMemory
		Settings      AISettings
		Revision      int64
		MaxSequence   int64
		TotalMessages int64
	}{conversation, result.Messages, result.Memories, settings, settingsRevision, maxSequence, totalMessages})
	if err != nil {
		return AIContext{}, ErrUnavailable
	}
	result.ContextHash = fingerprint("ai-context-v1", string(raw))
	return result, nil
}

func (s *Store) AIContext(conversationID string) (AIContext, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return AIContext{}, ErrUnavailable
	}
	defer tx.Rollback()
	result, err := aiContext(tx, conversationID)
	if err != nil {
		return AIContext{}, err
	}
	if tx.Commit() != nil {
		return AIContext{}, ErrUnavailable
	}
	return result, nil
}

func (s *Store) AISettings(conversationID string) (AISettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return AISettings{}, ErrUnavailable
	}
	defer tx.Rollback()
	if _, err = aiConversation(tx, conversationID); err != nil {
		return AISettings{}, err
	}
	settings, _, err := aiSettings(tx, conversationID)
	if err != nil {
		return AISettings{}, err
	}
	if tx.Commit() != nil {
		return AISettings{}, ErrUnavailable
	}
	return settings, nil
}

func bumpAIPreferences(tx *sql.Tx, conversation AIConversation, mode, tone string) error {
	_, err := tx.Exec(`INSERT INTO ai_preferences(conversation_id,workspace_id,connection_id,contact_id,mode,tone,revision)
 VALUES(?,?,?,?,?,?,1) ON CONFLICT(conversation_id) DO UPDATE SET mode=excluded.mode,tone=excluded.tone,revision=revision+1`, conversation.ID, workspaceID, conversation.ConnectionID, conversation.ContactID, mode, tone)
	if err != nil {
		return ErrUnavailable
	}
	return nil
}

func (s *Store) UpdateAISettings(conversationID, mode, tone string) error {
	if mode != "" && mode != "draft_only" && mode != "paused" || tone != "" && tone != "natural" && tone != "casual" && tone != "professional" || mode == "" && tone == "" {
		return ErrInvalidInput
	}
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
	settings, _, err := aiSettings(tx, conversationID)
	if err != nil {
		return err
	}
	if mode == "" {
		mode = settings.Mode
	}
	if tone == "" {
		tone = settings.Tone
	}
	if mode != settings.Mode || tone != settings.Tone {
		if err = bumpAIPreferences(tx, conversation, mode, tone); err != nil {
			return err
		}
	}
	if tx.Commit() != nil {
		return ErrUnavailable
	}
	return nil
}
