//go:build !governed

package tui

import "context"

// ownerPlanGate reports whether a drafted plan waits for the owner's
// approval. The shipped Marshal approves its own plans, so no plan gate
// is shown.
const ownerPlanGate = false

// readPendingContract is the project contract the live Marshal submitted
// that waits for the owner's browser Accept, nil when none does. Without a
// live Marshal launch nothing can bind the owner's Accept, so nothing waits.
func (w *Workspace) readPendingContract(ctx context.Context) (*gateContract, error) {
	w.mu.RLock()
	rt := w.runtime
	w.mu.RUnlock()
	if w.store == nil || rt == nil {
		return nil, nil
	}
	launch := w.proposalLaunchID()
	if launch == "" {
		return nil, nil
	}
	p, found, err := w.store.ReadPendingProjectContract(ctx, rt.ProjectContractBinding(), launch)
	if err != nil || !found {
		return nil, err
	}
	return &gateContract{ID: p.ID, Revision: p.Revision, Digest: p.Digest, Purpose: p.Purpose, Submitted: p.Submitted}, nil
}
