//go:build governed

package app

import (
	"testing"
	"time"
)

// The public edition keeps its reset parsing: a compound duration is not read.
func TestAgyQuotaCompoundResetUnchangedInCommunity(t *testing.T) {
	if d := parseCooldown("Individual quota reached. Resets in 100h11m27s.", time.Now()); d != defaultCooldown {
		t.Fatalf("community cooldown changed: %v", d)
	}
	if d, ok := parseCooldownText("Resets in 45m", time.Now()); !ok || d != 45*time.Minute {
		t.Fatalf("single-unit reset changed: %v %v", d, ok)
	}
}
