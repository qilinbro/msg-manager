// Package memory defines evidence-based retrieval within one conversation scope.
package memory

import (
	"context"

	"github.com/qilinbro/msg-manager/internal/domain"
)

type Fact struct {
	ID                string
	Scope             domain.ConversationScope
	Content           string
	VerificationState string
	Evidence          []domain.EvidenceRef
}

type Query struct {
	Scope domain.ConversationScope
	Text  string
	Limit int
}

// Retriever must filter by the entire scope before returning evidence to a model.
type Retriever interface {
	Retrieve(context.Context, Query) ([]Fact, error)
}
