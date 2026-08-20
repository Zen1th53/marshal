package webcontrol_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT197SecurityPolicyAndGateInspector(t *testing.T) {
	client := newAuthenticatedTestClient(t, "admin")

	// 1. Get Security Policy
	reqPolicy := httptest.NewRequest(http.MethodGet, "/api/v1/security/policy", nil)
	wPolicy := client.Do(reqPolicy)

	if wPolicy.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", wPolicy.Code)
	}

	var policyResp webcontrol.SecurityPolicyInspectorResponseDTO
	_ = json.NewDecoder(wPolicy.Body).Decode(&policyResp)

	if policyResp.PolicyID != "POL-MARSHAL-MAIN-2026" || len(policyResp.GateRules) < 4 {
		t.Fatalf("unexpected policy inspector data: %+v", policyResp)
	}

	// 2. Check capability evaluation rules and denial reasons
	foundDenied := false
	for _, c := range policyResp.CapabilityRules {
		if c.Decision == "DENIED" {
			foundDenied = true
			if c.DenialReason == "" {
				t.Fatalf("denied capability %s must have a concrete denial reason", c.CapabilityName)
			}
		}
	}
	if !foundDenied {
		t.Fatal("expected at least one denied capability in policy matrix")
	}

	// 3. Security invariant: Policy is read-only (arbitrary POST denied by CSRF or router)
	reqPost := httptest.NewRequest(http.MethodPost, "/api/v1/security/policy", nil)
	wPost := client.Do(reqPost)

	if wPost.Code != http.StatusMethodNotAllowed && wPost.Code != http.StatusNotFound && wPost.Code != http.StatusForbidden {
		t.Fatalf("expected 403, 404, or 405 for POST on read-only policy, got: %d", wPost.Code)
	}
}
