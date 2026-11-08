//go:build !governed

package tui

import "context"

// readPendingContract is the project contract the live Marshal submitted
// that waits for the owner's "accept architecture", nil when none does.
// Without a live Marshal launch nothing can bind the owner's reply, so
// nothing waits.
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
