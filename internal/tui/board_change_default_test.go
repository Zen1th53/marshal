//go:build governed

package tui

import (
	"context"
	"testing"
)

// In the governed edition an amend proposal that arrives while the run is
// busy and a change card is open is not diverted: it takes the old run
// proposal path with its operator approval, and the busy-apply sequence never
// runs.
func TestBusyAmendGovernedKeepsApprovalPath(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	w.change.card = &changeCard{runID: "run", notStarted: 1, applying: true}
	m := w.marshalSession()
	m.mu.Lock()
	m.busy = true
	m.mu.Unlock()
	called := false
	w.change.stopFn = func() string { called = true; return "" }
	w.change.busyFn = func() bool { called = true; return false }
	w.change.amendFn = func(context.Context, string) (bool, error) { called = true; return false, nil }
	w.change.resumeFn = func(context.Context) (string, error) { called = true; return "", nil }
	// With no bound run the old path refuses exactly as before the change.
	err := w.queueMarshalRunProposal(marshalProposal{action: "amend", reason: "switch to OAuth"})
	if err == nil || err.Error() != "no matching active plan" {
		t.Fatalf("the amend did not take the old path: %v", err)
	}
	if called {
		t.Fatal("the busy-apply sequence ran in the governed edition")
	}
	if w.currentChange() == nil {
		t.Fatal("the change card was closed by a diverted amend")
	}
}
