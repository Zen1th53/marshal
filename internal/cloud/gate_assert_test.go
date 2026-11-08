package cloud

import (
	"github.com/Zen1th53/marshal/internal/goalintake"
	"testing"
)

func assertNoULTRA(t *testing.T, g *Gate, attack string) {
	t.Helper()
	if g.Entitled() {
		t.Fatalf("%s: gate reported entitlement", attack)
	}
	if g.Mode() != goalintake.ModeStandard {
		t.Fatalf("%s: gate did not report Standard", attack)
	}
	if _, err := g.Bundle(); err == nil {
		t.Fatalf("%s: ULTRA material was available", attack)
	}
	// The decision that actually governs behaviour must refuse to delegate,
	// even with the local preference switched on.
	if goalintake.Confirm(goalintake.Intake{}, g.Mode(), g.Policy(true)).Delegated {
		t.Fatalf("%s: confirmation was delegated", attack)
	}
}
