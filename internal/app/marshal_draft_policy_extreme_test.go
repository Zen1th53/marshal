//go:build !governed

package app

import (
	"strings"
	"testing"
)

func TestMarshalExtremeDraftPolicyMatchesCanonicalAdmission(t *testing.T) {
	policy := marshalDraftTaskPolicy()
	for _, required := range []string{"mode governed", "exactly one acceptance criterion", "verified governed harness profile"} {
		if !strings.Contains(policy, required) {
			t.Fatalf("draft policy missing %q: %s", required, policy)
		}
	}
	if strings.Contains(policy, "defaults to native") {
		t.Fatalf("draft policy contradicts canonical admission: %s", policy)
	}
}
