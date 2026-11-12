package store

type Connection struct {
	ID           string `json:"id"`
	Platform     string `json:"platform"`
	AccountID    string `json:"account_id"`
	DisplayName  string `json:"display_name"`
	Source       string `json:"source"`
	MessageCount int    `json:"message_count"`
}

type Conversation struct {
	ID            string `json:"id"`
	ConnectionID  string `json:"connection_id"`
	Platform      string `json:"platform"`
	AccountID     string `json:"account_id"`
	AccountName   string `json:"account_name,omitempty"`
	PeerID        string `json:"peer_id"`
	Title         string `json:"title"`
	LastMessage   string `json:"last_message"`
	LastMessageAt string `json:"last_message_at"`
	UnreadCount   int    `json:"unread_count"`
	Pinned        bool   `json:"pinned"`
	Archived      bool   `json:"archived"`
}

type Message struct {
	ID             string        `json:"id"`
	ConversationID string        `json:"conversation_id"`
	ExternalID     string        `json:"external_id"`
	Direction      string        `json:"direction"`
	Body           string        `json:"body"`
	OccurredAt     string        `json:"occurred_at"`
	Read           bool          `json:"read"`
	Source         string        `json:"source"`
	Email          *EmailHeaders `json:"email,omitempty"`
}

type EmailHeaders struct {
	Subject     string   `json:"subject"`
	FromName    string   `json:"from_name"`
	FromAddress string   `json:"from_address"`
	To          []string `json:"to"`
	Folder      string   `json:"folder"`
}

type ImportMessage struct {
	ExternalID string        `json:"external_id"`
	PeerID     string        `json:"peer_id"`
	PeerName   string        `json:"peer_name"`
	Direction  string        `json:"direction"`
	Body       string        `json:"body"`
	OccurredAt string        `json:"occurred_at"`
	Read       *bool         `json:"read,omitempty"`
	Email      *EmailHeaders `json:"email,omitempty"`
}

type ImportRequest struct {
	Platform    string          `json:"platform"`
	AccountID   string          `json:"account_id"`
	AccountName string          `json:"account_name"`
	Messages    []ImportMessage `json:"messages"`
}

type ImportResult struct {
	Inserted   int `json:"inserted"`
	Duplicates int `json:"duplicates"`
	// Conversations is the number of distinct conversations touched by this
	// batch, including those for which all messages were already present.
	Conversations int `json:"conversations"`
}

type Filter struct {
	Q        string
	Platform string
	Unread   bool
	Archived bool
	Pinned   bool
	Cursor   string
	Limit    int
}

type ConversationPage struct {
	Items      []Conversation `json:"items"`
	NextCursor *string        `json:"next_cursor"`
}

type MessagePage struct {
	Items      []Message `json:"items"`
	NextCursor *string   `json:"next_cursor"`
}

type ConversationPatch struct {
	Read     *bool `json:"read,omitempty"`
	Pinned   *bool `json:"pinned,omitempty"`
	Archived *bool `json:"archived,omitempty"`
}
