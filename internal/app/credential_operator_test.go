package app

import "testing"

// requireOperatorCredentialDecisions marks a test of the operator (or Marshal)
// credential decision surface. Extreme applies registered provider
// credentials automatically, covered by credential_auto_extreme_test.go.
func requireOperatorCredentialDecisions(t *testing.T) {
	t.Helper()
	if automaticCredentialDecision {
		t.Skip("Marshal applies provider credentials automatically")
	}
}
