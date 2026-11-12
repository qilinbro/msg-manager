package domain

import "time"

type Message struct {
	ID                string            `json:"id"`
	Scope             ConversationScope `json:"scope"`
	ExternalMessageID string            `json:"external_message_id"`
	Version           uint64            `json:"version"`
	Direction         string            `json:"direction"`
	Body              string            `json:"body"`
	OccurredAt        time.Time         `json:"occurred_at"`
	ReceivedAt        time.Time         `json:"received_at"`
}

type EvidenceRef struct {
	MessageID      string `json:"message_id"`
	MessageVersion uint64 `json:"message_version"`
}
