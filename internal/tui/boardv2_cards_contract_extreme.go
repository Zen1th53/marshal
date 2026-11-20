//go:build !governed

package tui

import (
	"context"
	"errors"

	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// readDecisionContracts is the project contract waiting for the owner's
// Accept and the accepted one it revises (zero for a first draft), by the
// same reads the browser review is built from. found is false when nothing
// waits.
func (w *Workspace) readDecisionContracts(ctx context.Context) (pending, accepted projectcontext.Contract, found bool, err error) {
	w.mu.RLock()
	rt := w.runtime
	w.mu.RUnlock()
	if w.store == nil || rt == nil {
		return pending, accepted, false, nil
	}
	launch := w.proposalLaunchID()
	if launch == "" {
		return pending, accepted, false, nil
	}
	binding := rt.ProjectContractBinding()
	p, found, err := w.store.ReadPendingProjectContract(ctx, binding, launch)
	if err != nil || !found {
		return pending, accepted, false, err
	}
	current, err := w.store.ReadAcceptedProjectContract(ctx, binding)
	if err != nil && !errors.Is(err, projectcontext.ErrReviewPending) {
		return pending, accepted, false, err
	}
	return p.Contract, current.Contract, true, nil
}
