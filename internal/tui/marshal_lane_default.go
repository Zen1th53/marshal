//go:build governed

package tui

import (
	"context"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/permission"
)

func privateMarshalProposalKeys(marshalProposal) ([]string, bool) { return nil, false }
func privateProcess05ApprovalBound(context.Context, *app.ExecutionService, marshal.Run, execution.ExecutionRun, string, string) bool {
	return false
}
func (w *Workspace) pendingProcess05Approval(context.Context, marshal.Run, string) bool { return false }

func (*Workspace) describePrivateTransportApproval(*permission.Request, string) {}
