// Package agent describes model suggestions, without granting any sending tool.
package agent

import (
	"context"

	"github.com/qilinbro/msg-manager/internal/domain"
	"github.com/qilinbro/msg-manager/internal/memory"
)

type Input struct {
	Scope    domain.ConversationScope
	Message  domain.Message
	Evidence []memory.Fact
}

type Suggestion struct {
	Intent          string
	RiskFlags       []string
	DraftText       string
	Evidence        []domain.EvidenceRef
	SuggestedAction string
}

type Provider interface {
	Suggest(context.Context, Input) (Suggestion, error)
}
