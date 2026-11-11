//go:build governed

package tui

import "context"

// ownerPlanGate reports whether a drafted plan waits for the owner's
// approval. The governed build asks the owner to approve each plan.
const ownerPlanGate = true

// readPendingContract: the Community edition has no Marshal-submitted
// project contract, so no architecture gate waits.
func (w *Workspace) readPendingContract(context.Context) (*gateContract, error) {
	return nil, nil
}
