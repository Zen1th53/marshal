//go:build !governed

package execution

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

func TestRequiredConstraintDigestBindsAllFieldsAndPreservesLegacy(t *testing.T) {
	pkg := ConstraintPackage{GoalOutcome: "Preserve history", TaskID: "inspect", TaskDescription: "Inspect the approved result", HardConstraints: []string{"Preserve evidence"}, DoNotDo: []string{"Do not publish"}, CapabilityLimits: []string{"Read only"}, ExpectedOutputs: []string{"report"}, VerificationObligations: []string{"Tests pass"}}
	legacy := ComputeConstraintDigest(pkg)
	raw, _ := json.Marshal(pkg)
	if strings.Contains(string(raw), "required_context") {
		t.Fatal("nil context changed legacy JSON")
	}
	binding := projectcontext.Binding{FormatVersion: 1, Project: projectcontext.ProjectBinding{CanonicalID: "canonical", StorageID: "storage"}, Contract: projectcontext.ContractRef{ID: "contract", Revision: 1, Digest: projectcontext.Digest("contract", "accepted")}, InventoryDigest: projectcontext.Digest("inventory", "current"), ManifestDigest: projectcontext.Digest("manifest", "complete"), RequiredStateDigest: projectcontext.Digest("state", "current"), GoalID: "goal", GoalRevision: 1, PlanID: "plan", PlanVersion: 1, ApprovalScopeDigest: (plan.ApprovalScope{}).Digest(), RunID: "run", RunVersion: 1, TaskID: "inspect", CanonicalTaskID: "TASK-inspect", Principal: "worker", Session: "session", Provider: "codex", Model: "selected-model", Attempt: 1, Base: "base", Tree: "tree"}
	pkg.RequiredContext = &binding
	pkg.Digest = ComputeConstraintDigest(pkg)
	if pkg.Digest == legacy {
		t.Fatal("required context omitted from canonical digest")
	}
	if err := VerifyConstraintPackage(pkg); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*ConstraintPackage)
	}{
		{"expected_output", func(p *ConstraintPackage) { p.ExpectedOutputs = []string{"different"} }},
		{"capability_limit", func(p *ConstraintPackage) { p.CapabilityLimits = []string{"Write allowed"} }},
		{"manifest", func(p *ConstraintPackage) {
			p.RequiredContext.ManifestDigest = projectcontext.Digest("manifest", "omitted")
		}},
		{"principal", func(p *ConstraintPackage) { p.RequiredContext.Principal = "foreign" }},
		{"task", func(p *ConstraintPackage) { p.RequiredContext.TaskID = "foreign" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			bad := pkg
			b := binding
			bad.RequiredContext = &b
			test.change(&bad)
			if VerifyConstraintPackage(bad) == nil {
				t.Fatal("bound constraint mutation accepted")
			}
		})
	}
	bad := pkg
	b := binding
	bad.RequiredContext = &b
	bad.RequiredContext.ManifestDigest = ""
	bad.Digest = ComputeConstraintDigest(bad)
	if VerifyConstraintPackage(bad) == nil {
		t.Fatal("incomplete binding accepted even with recomputed digest")
	}
}

func TestRequiredConstraintAcceptsActualApprovalScopeDigestAndRefusesUnknownDigests(t *testing.T) {
	pkg := ConstraintPackage{GoalOutcome: "Preserve history", TaskDescription: "Inspect approved work", TaskID: "inspect", RequiredContext: &projectcontext.Binding{FormatVersion: 1, Project: projectcontext.ProjectBinding{CanonicalID: "canonical", StorageID: "storage"}, Contract: projectcontext.ContractRef{ID: "contract", Revision: 1, Digest: projectcontext.Digest("contract", "accepted")}, InventoryDigest: projectcontext.Digest("inventory", "current"), ManifestDigest: projectcontext.Digest("manifest", "complete"), RequiredStateDigest: projectcontext.Digest("state", "current"), GoalID: "goal", GoalRevision: 1, PlanID: "plan", PlanVersion: 1, ApprovalScopeDigest: (plan.ApprovalScope{}).Digest(), RunID: "run", RunVersion: 1, TaskID: "inspect", CanonicalTaskID: "TASK-inspect", Principal: "worker", Session: "session", Provider: "codex", Model: "selected-model", Attempt: 1, Base: "base", Tree: "tree"}}
	pkg.Digest = ComputeConstraintDigest(pkg)
	if err := VerifyConstraintPackage(pkg); err != nil {
		t.Errorf("actual canonical approval scope refused: %v", err)
	}
	for _, fault := range []string{"nonhex", "missing_required_state", "missing_model", "wrong_scope_length"} {
		t.Run(fault, func(t *testing.T) {
			bad := pkg
			b := *pkg.RequiredContext
			bad.RequiredContext = &b
			switch fault {
			case "nonhex":
				b.ManifestDigest = "sha256:" + strings.Repeat("z", 64)
			case "missing_required_state":
				b.RequiredStateDigest = ""
			case "missing_model":
				b.Model = ""
			case "wrong_scope_length":
				b.ApprovalScopeDigest = "sha256:abcd"
			}
			bad.Digest = ComputeConstraintDigest(bad)
			if VerifyConstraintPackage(bad) == nil {
				t.Fatal("unqualified binding accepted after recomputing package")
			}
		})
	}
}
