//go:build !governed

package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/store"
)

// During intake, with a confirmed Goal but no accepted architecture yet,
// marshal_project_contract_context is not a failure: it answers that the
// architecture still waits for the owner's browser Accept and what to do
// next, as data only. It mints no planning proof, so planning stays blocked
// until the architecture is accepted. It used to fail with "The project
// context is not ready yet", which the Marshal chat showed as a failed call.
func TestNativePlanningContextBeforeArchitectureAcceptedIsNotAFailure(t *testing.T) {
	for _, submitted := range []bool{false, true} {
		name := "not submitted"
		if submitted {
			name = "submitted, not accepted"
		}
		t.Run(name, func(t *testing.T) {
			w, lease, cfg := liveMCPFixture(t)
			goal := model.GoalContract{ID: "GOAL-arch-pending", SessionID: w.sessionID, ProjectID: w.runtime.ProjectIdentity(), OriginalRequest: "Create hello.py", ConstitutionVersion: constitution.Current.String(), DesiredOutcome: "Create hello.py", ExpectedArtifact: "hello.py", SuccessCriteria: []string{"hello.py prints Hello"}, Risk: model.R0, AuthoritySource: "operator", Confirmation: model.ConfirmationApproved, Assessment: map[string]string{"complexity": "LOW", "ambiguity": "LOW", "blast_radius": "LOW", "privilege": "LOW", "reversibility": "LOW", "external_effects": "LOW", "data_sensitivity": "LOW", "dependency_depth": "LOW", "verification_difficulty": "LOW", "operational_criticality": "LOW"}}
			if err := w.store.SaveGoalContract(t.Context(), goal, 0); err != nil {
				t.Fatal(err)
			}
			if submitted {
				now := time.Now().UTC().Add(-time.Second)
				record := model.MemoryRecordV2{ID: "MEM-arch-pending", ProjectID: w.projectID, Revision: 1, Kind: model.MemoryKindDecision, Lifecycle: model.MemoryDurable, Authority: model.AuthorityOperator, Confidence: model.ConfidenceVerified, Title: "Policy", Body: "Keep it simple.", Scope: "project", ScopeID: w.projectID, ObservedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now, Source: model.MemorySource{Kind: "runtime_service", Reference: "owner"}}
				record.ContentDigest = record.CanonicalDigest()
				if err := w.store.WriteMemoryV2(t.Context(), record); err != nil {
					t.Fatal(err)
				}
				child := memoryChild(t, w.runtime.ProjectRoot(), &cfg)
				var inventory store.ProjectContractInventoryPage
				if err := json.Unmarshal([]byte(child.call(t, "marshal_project_contract_inventory", map[string]any{"limit": 1}, true)), &inventory); err != nil || len(inventory.Entries) != 1 {
					t.Fatal("inventory", err)
				}
				e := inventory.Entries[0]
				draft := store.ProjectContractSubmission{Action: "project-contract", Contract: projectcontext.Contract{FormatVersion: 1, Project: w.runtime.ProjectContractBinding(), ID: "ARCH-arch-pending", Revision: 1, Purpose: "Say hello", Components: []projectcontext.Component{{ID: "core", Responsibility: "Print", Paths: []string{"src"}}}, Invariants: []string{"Print exactly once"}, ReviewedInventoryDigest: inventory.CompletenessDigest, Required: []projectcontext.MemoryRef{{ID: e.ID, ProjectID: e.ProjectID, Revision: e.Revision, Scope: e.Scope, ScopeID: e.ScopeID, ContentDigest: e.ContentDigest, StateFingerprint: e.StateFingerprint, Class: projectcontext.Policy, Applies: projectcontext.Applicability{TaskIDs: []string{"future-task"}}, AcceptanceOccurrence: "owner-policy", Reason: "Required"}}}}
				child.call(t, "marshal_project_contract_submit", map[string]any{"submission_id": "arch-pending-contract", "proposal": draft}, true)
			}
			w.proposalMCP.mu.Lock()
			raw, err := w.readNativePlanningMCP(t.Context(), lease)
			minted := lease.planning != nil
			w.proposalMCP.mu.Unlock()
			if err != nil {
				t.Fatalf("before the architecture is accepted the context read failed: %v", err)
			}
			if minted {
				t.Fatal("a read before the architecture is accepted minted a planning proof")
			}
			var got struct {
				Missing string `json:"missing"`
				Next    string `json:"next"`
			}
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			if got.Missing != "architecture" || !strings.Contains(got.Next, "browser") || !strings.Contains(got.Next, "marshal_project_contract_submit") {
				t.Fatalf("reply %s", raw)
			}
			if strings.Contains(got.Next, "is open in") {
				t.Fatalf("the next step claims the page is already open: %q", got.Next)
			}
		})
	}
}
