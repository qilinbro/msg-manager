// Package policy owns execution permission. Model suggestions cannot authorize sends.
package policy

import (
	"context"

	"github.com/qilinbro/msg-manager/internal/domain"
)

type SendRequest struct {
	Scope         domain.ConversationScope
	OperationID   string
	PolicyVersion uint64
	BodyHash      string
}

type Decision struct {
	Allowed bool
	Code    string
}

type Authorizer interface {
	AuthorizeSend(context.Context, string, SendRequest) (Decision, error)
}

// DenyAll is the only scaffold policy; it has no option to enable sending.
type DenyAll struct{}

var _ Authorizer = DenyAll{}

func (DenyAll) AuthorizeSend(ctx context.Context, authenticatedWorkspace string, req SendRequest) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{Code: "CANCELLED"}, err
	}
	if err := req.Scope.AssertWorkspace(authenticatedWorkspace); err != nil {
		return Decision{Code: "SCOPE_DENIED"}, err
	}
	return Decision{Code: "SCAFFOLD_SEND_DISABLED"}, nil
}
