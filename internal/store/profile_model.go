package store

// Profiles are scoped through the existing conversation/contact identity graph.
// No source credentials or plaintext files are part of this model.
type Profile struct {
	ConversationID      string          `json:"conversation_id"`
	ContactID           string          `json:"contact_id"`
	DisplayName         string          `json:"display_name"`
	Platform            string          `json:"platform"`
	PeerID              string          `json:"peer_id"`
	PublicName          string          `json:"public_name"`
	Company             string          `json:"company"`
	Role                string          `json:"role"`
	ManualNotes         string          `json:"manual_notes"`
	CategoryOverride    string          `json:"category_override"`
	AutoOrganize        bool            `json:"auto_organize"`
	PublicSearchEnabled bool            `json:"public_search_enabled"`
	AIPaused            bool            `json:"ai_paused"`
	ModelConfigID       string          `json:"model_config_id"`
	Revision            int64           `json:"revision"`
	ProcessedSeq        int64           `json:"processed_seq"`
	MaxSeq              int64           `json:"max_seq"`
	AnalysisTotal       int64           `json:"analysis_total"`
	AnalysisProcessed   int64           `json:"analysis_processed"`
	Category            string          `json:"category"`
	AICategory          string          `json:"ai_category"`
	Tags                []string        `json:"tags"`
	Summary             string          `json:"summary"`
	EvidenceIDs         []string        `json:"evidence_ids"`
	Facts               []ProfileFact   `json:"facts"`
	Sources             []ProfileSource `json:"sources"`
	Jobs                []ProfileJob    `json:"jobs"`
	UpdatedAt           *string         `json:"updated_at"`
	DocumentMarkdown    string          `json:"document_markdown"`
	Truncated           bool            `json:"truncated"`
}

type ProfilePatch struct {
	ExpectedRevision    *int64  `json:"expected_revision"`
	PublicName          *string `json:"public_name"`
	Company             *string `json:"company"`
	Role                *string `json:"role"`
	ManualNotes         *string `json:"manual_notes"`
	CategoryOverride    *string `json:"category_override"`
	AutoOrganize        *bool   `json:"auto_organize"`
	PublicSearchEnabled *bool   `json:"public_search_enabled"`
	// The backend binds authorization to its current model configuration.
	ModelConfigID *string `json:"-"`
}

type ProfileFact struct {
	ID         string   `json:"id"`
	Field      string   `json:"field"`
	Value      string   `json:"value"`
	State      string   `json:"state"`
	Origin     string   `json:"origin"`
	MessageIDs []string `json:"message_ids"`
	SourceIDs  []string `json:"source_ids"`
}

type ProfileSource struct {
	ID          string `json:"id"`
	URL         string `json:"url"`
	Title       string `json:"title"`
	Snippet     string `json:"snippet"`
	ContentHash string `json:"content_hash"`
	RetrievedAt string `json:"retrieved_at"`
}

type ProfileJob struct {
	ID               string  `json:"id"`
	ConversationID   string  `json:"conversation_id"`
	Kind             string  `json:"kind"`
	State            string  `json:"state"`
	Attempts         int     `json:"attempts"`
	NextAttemptAt    *string `json:"next_attempt_at"`
	LeaseUntil       *string `json:"lease_until"`
	LeaseToken       string  `json:"-"`
	Fence            int64   `json:"-"`
	ModelConfigID    string  `json:"model_config_id"`
	SettingsRevision int64   `json:"settings_revision"`
	MaxSeq           int64   `json:"max_seq"`
	LastErrorCode    *string `json:"last_error_code"`
	UpdatedAt        string  `json:"updated_at"`
	Manual           bool    `json:"-"`
}

type ProfileJobInput struct {
	Profile     Profile     `json:"profile"`
	Messages    []AIMessage `json:"messages"`
	ContextHash string      `json:"context_hash"`
	Truncated   bool        `json:"truncated"`
	// ProcessedThroughSeq is local cursor metadata and never enters provider JSON.
	ProcessedThroughSeq int64 `json:"-"`
}

type ProfileFactInput struct {
	Field      string   `json:"field"`
	Value      string   `json:"value"`
	MessageIDs []string `json:"message_ids"`
	SourceIDs  []string `json:"source_ids"`
}

type ProfileJobResult struct {
	Category    string             `json:"category"`
	Tags        []string           `json:"tags"`
	Summary     string             `json:"summary"`
	EvidenceIDs []string           `json:"evidence_ids"`
	SourceIDs   []string           `json:"source_ids"`
	Facts       []ProfileFactInput `json:"facts"`
	Sources     []ProfileSource    `json:"sources"`
	// ProcessedThroughSeq is local cursor metadata and never enters provider JSON.
	ProcessedThroughSeq int64 `json:"-"`
}
