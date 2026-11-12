package agent

const MaxInputBytes = 32000

type Config struct {
	BaseURL string
	APIKey  string
	Model   string
}

func (Config) String() string   { return "model configuration (redacted)" }
func (Config) GoString() string { return "model configuration (redacted)" }

type Evidence struct {
	ID         string `json:"id"`
	Body       string `json:"body"`
	Direction  string `json:"direction"`
	OccurredAt string `json:"occurred_at"`
}

type Memory struct {
	ID                string   `json:"id"`
	Content           string   `json:"content"`
	VerificationState string   `json:"verification_state"`
	EvidenceIDs       []string `json:"evidence_ids"`
}

type Input struct {
	ConversationID string     `json:"conversation_id"`
	Tone           string     `json:"tone"`
	Messages       []Evidence `json:"messages"`
	Memories       []Memory   `json:"memories"`
}

type MemoryCandidate struct {
	Content     string   `json:"content"`
	EvidenceIDs []string `json:"evidence_ids"`
}

type Output struct {
	Intent           string            `json:"intent"`
	Summary          string            `json:"summary"`
	Draft            string            `json:"draft"`
	RiskFlags        []string          `json:"risk_flags"`
	EvidenceIDs      []string          `json:"evidence_ids"`
	MemoryCandidates []MemoryCandidate `json:"memory_candidates"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

type Result struct {
	Output Output `json:"output"`
	Usage  Usage  `json:"usage"`
}

type Kind string

const (
	NotConfigured Kind = "not_configured"
	InvalidConfig Kind = "invalid_config"
	InvalidInput  Kind = "invalid_input"
	AuthFailed    Kind = "auth_failed"
	QuotaExceeded Kind = "quota_exceeded"
	Unavailable   Kind = "unavailable"
	Timeout       Kind = "timeout"
	InvalidOutput Kind = "invalid_output"
)

type Error struct{ Kind Kind }

func (e *Error) Error() string { return "model request failed: " + string(e.Kind) }
func failure(kind Kind) error  { return &Error{Kind: kind} }
