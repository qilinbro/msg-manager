package qqmilky

import "encoding/json"

type Verification struct {
	AccountID      string `json:"account_id"`
	Nickname       string `json:"nickname"`
	ImplName       string `json:"impl_name"`
	ImplVersion    string `json:"impl_version"`
	MilkyVersion   string `json:"milky_version"`
	ProtocolType   string `json:"qq_protocol_type"`
	SelfIDVerified bool   `json:"self_id_verified"`
}

type Friend struct {
	UserID   json.RawMessage `json:"user_id"`
	Nickname string          `json:"nickname"`
	Remark   string          `json:"remark"`
}

type Segment struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// IncomingMessage is the union payload of a message_receive event and an
// entry of get_history_messages. IDs stay as RawMessage so large integers are
// never decoded through float64.
type IncomingMessage struct {
	Scene      string          `json:"message_scene"`
	PeerID     json.RawMessage `json:"peer_id"`
	MessageSeq json.RawMessage `json:"message_seq"`
	SenderID   json.RawMessage `json:"sender_id"`
	Time       int64           `json:"time"`
	Segments   []Segment       `json:"segments"`
	Friend     *Friend         `json:"friend,omitempty"`
}

type Event struct {
	EventType string          `json:"event_type"`
	Time      int64           `json:"time"`
	SelfID    json.RawMessage `json:"self_id"`
	Data      json.RawMessage `json:"data"`
}

// NormalizedMessage is the product-neutral representation consumed by the API
// layer. SkipReason is non-empty when the event was persisted as a skipped
// event instead of a chat message.
type NormalizedMessage struct {
	ExternalID  string
	PeerID      string
	PeerName    string
	Direction   string
	Body        string
	OccurredAt  string
	MessageSeq  string
	Scene       string
	SkipReason  string
	Unsupported []string
}

type SendReceipt struct {
	MessageSeq string
	Time       int64
}

type Gap struct {
	At     int64
	Reason string
}
