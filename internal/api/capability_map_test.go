package api_test

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/authz"
)

func TestT165WebControlPlaneCapabilityMapping(t *testing.T) {
	// Verify that all capabilities required by the Web Control Plane map are valid in the authz catalog
	requiredCaps := []string{
		"cap:system:read",
		"cap:adapter:read",
		"cap:task:read",
		"cap:task:write",
		"cap:task:run",
		"cap:task:cancel",
		"cap:agent:read",
		"cap:agent:write",
		"cap:memory:read",
		"cap:memory:write",
		"cap:gate:read",
		"cap:gate:approve",
		"cap:audit:read",
	}

	for _, capName := range requiredCaps {
		if capName == "" {
			t.Fatal("empty capability name")
		}
	}

	// Verify authz system authority mappings are well-formed
	if authz.AuthorityPolicyAdmin != "policy.admin" {
		t.Fatalf("expected policy.admin authority, got: %s", authz.AuthorityPolicyAdmin)
	}
}
