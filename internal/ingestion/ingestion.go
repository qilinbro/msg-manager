// Package ingestion defines the durable inbox transaction boundary.
package ingestion

import (
	"context"

	"github.com/qilinbro/msg-manager/internal/connector"
	"github.com/qilinbro/msg-manager/internal/domain"
)

type CommitResult struct {
	InsertedEvents int
	DurableCursor  string
}

// Store implementations must atomically persist inbox event deduplication,
// normalized messages, follow-up jobs and the cursor. The receiver may acknowledge
// a platform cursor only after this commit succeeds. No implementation exists yet.
type Store interface {
	CommitBatch(context.Context, domain.ConnectionScope, connector.Batch) (CommitResult, error)
}
