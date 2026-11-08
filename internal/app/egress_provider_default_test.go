//go:build governed

package app

import "testing"

func TestCommunityProviderEgressRequiresExactGrants(t *testing.T) {
	testProviderEgressAutomaticDecisions(t, false)
}

// openEgressEdition reports whether this edition grants every TCP
// destination of a governed run automatically.
const openEgressEdition = false
