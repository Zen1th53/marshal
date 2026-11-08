//go:build governed

package tui

import (
	"context"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/permission"
)

func (w *Workspace) queuePrivateProcess05Continuation(marshal.Run) {}
func (w *Workspace) queuePrivateProcess05Resume()                  {}

func (*Workspace) describePrivateProcess05Resume(*permission.Request, marshalProposal) bool {
	return true
}
func (*Workspace) validatePrivateProcess05Resume(context.Context, permission.Request, marshalProposal) error {
	return nil
}

func (*Workspace) privateProposalMCPApplyContext(ctx context.Context, _ permission.Request) (context.Context, func()) {
	return ctx, func() {}
}
