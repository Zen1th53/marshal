//go:build governed

package tui

import (
	"context"

	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// readDecisionContracts: this edition has no Marshal-submitted project
// contract (readPendingContract), so no architecture waits for an Accept.
func (w *Workspace) readDecisionContracts(context.Context) (pending, accepted projectcontext.Contract, found bool, err error) {
	return pending, accepted, false, nil
}
