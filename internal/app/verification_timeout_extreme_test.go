//go:build !governed

package app

import "testing"

// TestExtremeVerificationHasNoWallClockLimit: Extreme repository
// verification is never killed by a wall-clock limit.
func TestExtremeVerificationHasNoWallClockLimit(t *testing.T) {
	if verificationTimeout != 0 {
		t.Fatalf("verificationTimeout = %v, want 0 (no limit) in Marshal", verificationTimeout)
	}
}
