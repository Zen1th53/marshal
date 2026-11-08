//go:build governed

package tui

import "context"

// readPendingContract: the Community edition has no Marshal-submitted
// project contract, so no architecture gate waits.
func (w *Workspace) readPendingContract(context.Context) (*gateContract, error) {
	return nil, nil
}
