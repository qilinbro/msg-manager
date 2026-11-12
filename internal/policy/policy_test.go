package policy_test

import (
	"context"
	"errors"
	"testing"

	"github.com/qilinbro/msg-manager/internal/domain"
	"github.com/qilinbro/msg-manager/internal/policy"
)

func TestScaffoldNeverAuthorizesSending(t *testing.T) {
	scope := domain.ConversationScope{ConnectionScope: domain.ConnectionScope{WorkspaceID: domain.LocalWorkspaceID, ConnectionID: "connection"}, ContactID: "contact", ConversationID: "conversation"}
	decision, err := (policy.DenyAll{}).AuthorizeSend(context.Background(), domain.LocalWorkspaceID, policy.SendRequest{Scope: scope, OperationID: "operation", PolicyVersion: 99, BodyHash: "hash"})
	if err != nil || decision.Allowed || decision.Code != "SCAFFOLD_SEND_DISABLED" {
		t.Fatalf("expected explicit default denial, got decision=%+v error=%v", decision, err)
	}
	for _, authWorkspace := range []string{"", "someone-else"} {
		decision, err := (policy.DenyAll{}).AuthorizeSend(context.Background(), authWorkspace, policy.SendRequest{Scope: scope})
		if !errors.Is(err, domain.ErrScopeDenied) || decision.Allowed {
			t.Fatal("accepted mismatched authenticated workspace")
		}
	}
	scope.ContactID = ""
	decision, err = (policy.DenyAll{}).AuthorizeSend(context.Background(), domain.LocalWorkspaceID, policy.SendRequest{Scope: scope})
	if !errors.Is(err, domain.ErrScopeDenied) || decision.Allowed {
		t.Fatal("accepted an incomplete contact scope")
	}
}
