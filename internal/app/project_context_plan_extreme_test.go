//go:build !governed

package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
)

func TestRequiredPlanPinsAcceptedArchitectureInCanonicalApprovalScope(t *testing.T) {
	r, binding, _, _ := requiredContextFixture(t)
	request := planCreateRequest()
	request.ProjectID, request.SessionID = projectid.ID(r.ProjectIdentity()), binding.Session
	p, err := r.Plans().Create(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if p.RequiredProjectContract == nil || *p.RequiredProjectContract != binding.Contract {
		t.Fatal("canonical plan lost accepted architecture")
	}
	approved, err := r.Plans().Approve(t.Context(), request.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if approved.ApprovedScope.RequiredProjectContract == nil || *approved.ApprovedScope.RequiredProjectContract != binding.Contract {
		t.Fatal("approval omitted architecture")
	}
	before := approved.ApprovedScope.Digest()
	changed := *approved.ApprovedScope.RequiredProjectContract
	changed.Revision++
	approved.RequiredProjectContract = &changed
	if _, err := p.Approve(p.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	approved.ApprovedScope.RequiredProjectContract = &changed
	if approved.ApprovedScope.Digest() == before {
		t.Fatal("architecture change retained old scope digest")
	}
	raw, _ := json.Marshal(plan.ExecutionPlan{})
	if strings.Contains(string(raw), "required_project_contract") {
		t.Fatal("nil binding changed legacy wire")
	}
}
