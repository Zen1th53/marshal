//go:build governed

package app

import (
	"testing"
	"time"
)

// TestDefaultVerificationTimeoutStaysBounded: Community keeps its 15-minute
// bound on repository verification.
func TestDefaultVerificationTimeoutStaysBounded(t *testing.T) {
	if verificationTimeout != 15*time.Minute {
		t.Fatalf("verificationTimeout = %v, want 15m in Community", verificationTimeout)
	}
}
