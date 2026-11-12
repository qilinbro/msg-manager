// Package outbox defines future durable sending; no executor is installed yet.
package outbox

import (
	"context"

	"github.com/qilinbro/msg-manager/internal/connector"
	"github.com/qilinbro/msg-manager/internal/domain"
)

type State string

const (
	Pending   State = "pending"
	Sending   State = "sending"
	Sent      State = "sent"
	Failed    State = "failed"
	Unknown   State = "unknown"
	Cancelled State = "cancelled"
)

type Operation struct {
	ID            string
	Scope         domain.ConversationScope
	DedupeKey     string
	BodyHash      string
	PolicyVersion uint64
	State         State
}

// Store must enforce scope, dedupe keys and lease tokens transactionally.
// Startup recovery must turn abandoned sending operations into unknown,
// never pending. Unknown operations require explicit human reconciliation.
type Store interface {
	Enqueue(context.Context, Operation) error
	Claim(context.Context, domain.ConnectionScope) (Operation, string, error)
	RecordResult(context.Context, domain.ConversationScope, string, string, connector.SendResult) error
}
