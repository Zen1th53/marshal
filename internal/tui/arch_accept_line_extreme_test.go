//go:build linux && !governed

package tui

import (
	"context"
	"testing"
)

// The Accept architecture card says what accepting authorises in this
// build: the work starts and the Marshal approves its own plan.
func TestArchitectureCardSaysAcceptStartsWork(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	c := walkContract()
	w.gateContractSource = func(context.Context) (*gateContract, error) { return c, nil }
	w.refreshGates(t.Context())
	d := w.boardV2().Decision
	if d == nil || d.Kind != decisionArchitecture {
		t.Fatalf("no architecture card: %+v", d)
	}
	for _, l := range d.Lines {
		if l.Text == architectureAcceptStarts {
			return
		}
	}
	t.Fatalf("the card does not say what accepting starts: %+v", d.Lines)
}
