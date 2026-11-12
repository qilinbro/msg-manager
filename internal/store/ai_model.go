package store

import "errors"

var (
	ErrStaleContext  = errors.New("AI context changed; reload before continuing")
	ErrQuotaExceeded = errors.New("daily AI call quota exhausted")
)

type AIConversation struct {
	ID           string `json:"id"`
	ConnectionID string `json:"connection_id"`
	ContactID    string `json:"contact_id"`
	Platform     string `json:"platform"`
	Title        string `json:"title"`
}

type AIMessage struct {
	ID         string `json:"id"`
	Body       string `json:"body"`
	Direction  string `json:"direction"`
	OccurredAt string `json:"occurred_at"`
}

type AISettings struct {
	Mode string `json:"mode"`
	Tone string `json:"tone"`
}

type AIContext struct {
	Conversation AIConversation `json:"conversation"`
	ContextHash  string         `json:"context_hash"`
	Messages     []AIMessage    `json:"messages"`
	Memories     []AIMemory     `json:"memories"`
	Settings     AISettings     `json:"settings"`
	Truncated    bool           `json:"truncated"`
}

type AIMemory struct {
	ID                string   `json:"id"`
	ConversationID    string   `json:"conversation_id"`
	Content           string   `json:"content"`
	VerificationState string   `json:"verification_state"`
	EvidenceIDs       []string `json:"evidence_ids"`
	CreatedAt         string   `json:"created_at"`
	UpdatedAt         string   `json:"updated_at"`
}

type AICandidate struct {
	Content     string   `json:"content"`
	EvidenceIDs []string `json:"evidence_ids"`
}

type AIResult struct {
	Intent           string        `json:"intent"`
	Summary          string        `json:"summary"`
	Draft            string        `json:"draft"`
	Model            string        `json:"model"`
	ConfigID         string        `json:"config_id,omitempty"`
	RiskFlags        []string      `json:"risk_flags"`
	EvidenceIDs      []string      `json:"evidence_ids"`
	MemoryCandidates []AICandidate `json:"memory_candidates"`
	PromptTokens     int           `json:"prompt_tokens"`
	CompletionTokens int           `json:"completion_tokens"`
}

type AIAnalysis struct {
	ID             string `json:"id"`
	ConversationID string `json:"conversation_id"`
	ContextHash    string `json:"context_hash"`
	AIResult
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
	DraftEdited bool   `json:"draft_edited"`
	Stale       bool   `json:"stale"`
}
