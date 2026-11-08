//go:build !governed

package app

import "testing"

func TestExtremeProviderEgressAutomaticDecisions(t *testing.T) {
	testProviderEgressAutomaticDecisions(t, true)
}

func TestExtremeProviderProbeEgressAutomaticDecisions(t *testing.T) {
	testProviderEgressAutomaticDecisions(t, true, true)
}

// openEgressEdition reports whether this edition grants every TCP
// destination of a governed run automatically.
const openEgressEdition = true
