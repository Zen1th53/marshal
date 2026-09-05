package webcontrol_test

import (
	"bytes"
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

func TestGovernedPolicyWorkflow(t *testing.T) {
	client := newAuthenticatedTestClient(t, "admin")

	validYAML := `id: POL-MARSHAL-MAIN-2026
version: 2
default: deny
rules:
  - id: rule-task-create
    description: Allow operator to create tasks
    when:
      action: cap:task:create
      role: operator
    effect: allow
    priority: 10
  - id: rule-custom-test-governed
    description: Governed rule for verification
    when:
      action: cap:custom:test
      role: operator
    effect: allow
    priority: 15
`

	// 1. Save draft
	saveDraftPayload := webcontrol.MutationEnvelope[struct {
		YAMLContent string `json:"yaml_content"`
		PolicyID    string `json:"policy_id,omitempty"`
		Version     int    `json:"version,omitempty"`
	}]{
		Payload: struct {
			YAMLContent string `json:"yaml_content"`
			PolicyID    string `json:"policy_id,omitempty"`
			Version     int    `json:"version,omitempty"`
		}{
			YAMLContent: validYAML,
		},
	}
	body, _ := json.Marshal(saveDraftPayload)
	reqDraft := httptest.NewRequest(http.MethodPost, "/api/v1/security/policy/draft", bytes.NewReader(body))
	reqDraft.Header.Set("Content-Type", "application/json")
	wDraft := client.Do(reqDraft)
	if wDraft.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for save draft, got: %d: %s", wDraft.Code, wDraft.Body.String())
	}

	// 2. Validate policy
	valPayload := webcontrol.MutationEnvelope[struct {
		YAMLContent string `json:"yaml_content"`
	}]{
		Payload: struct {
			YAMLContent string `json:"yaml_content"`
		}{
			YAMLContent: validYAML,
		},
	}
	bodyVal, _ := json.Marshal(valPayload)
	reqVal := httptest.NewRequest(http.MethodPost, "/api/v1/security/policy/validate", bytes.NewReader(bodyVal))
	reqVal.Header.Set("Content-Type", "application/json")
	wVal := client.Do(reqVal)
	if wVal.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for validate policy, got: %d: %s", wVal.Code, wVal.Body.String())
	}
	var valResult webcontrol.PolicyValidationResultDTO
	_ = json.NewDecoder(wVal.Body).Decode(&valResult)
	if !valResult.Valid || valResult.RulesCount != 2 {
		t.Fatalf("unexpected validation result: %+v", valResult)
	}

	// 3. Diff policy
	reqDiff := httptest.NewRequest(http.MethodPost, "/api/v1/security/policy/diff", bytes.NewReader(bodyVal))
	reqDiff.Header.Set("Content-Type", "application/json")
	wDiff := client.Do(reqDiff)
	if wDiff.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for policy diff, got: %d: %s", wDiff.Code, wDiff.Body.String())
	}
	var diffResult webcontrol.PolicyDiffDTO
	_ = json.NewDecoder(wDiff.Body).Decode(&diffResult)
	if !diffResult.HasChanges {
		t.Fatalf("expected changes in diff result, got: %+v", diffResult)
	}

	// 4. Apply policy
	applyPayload := webcontrol.MutationEnvelope[struct {
		ExpectedRevision *int   `json:"expected_revision,omitempty"`
		DraftDigest      string `json:"draft_digest,omitempty"`
	}]{
		Payload: struct {
			ExpectedRevision *int   `json:"expected_revision,omitempty"`
			DraftDigest      string `json:"draft_digest,omitempty"`
		}{
			DraftDigest: valResult.Digest,
		},
	}
	bodyApply, _ := json.Marshal(applyPayload)
	reqApply := httptest.NewRequest(http.MethodPost, "/api/v1/security/policy/apply", bytes.NewReader(bodyApply))
	reqApply.Header.Set("Content-Type", "application/json")
	wApply := client.Do(reqApply)
	if wApply.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for policy apply, got: %d: %s", wApply.Code, wApply.Body.String())
	}

	// 5. Rollback policy
	reqRollback := httptest.NewRequest(http.MethodPost, "/api/v1/security/policy/rollback", nil)
	wRollback := client.Do(reqRollback)
	if wRollback.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for policy rollback, got: %d: %s", wRollback.Code, wRollback.Body.String())
	}
}
