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
	// The delegation policy must not be entitled, even with the local
	// preference switched on.
	if g.Policy(true).Entitled {
		t.Fatalf("%s: delegation policy was entitled", attack)
	}
}
