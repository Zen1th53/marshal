//go:build !governed

package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestProjectContractNativeCompleteContextIncludesFutureTaskMemoryAndRefusesRevocation(t *testing.T) {
	w, lease, cfg := liveMCPFixture(t)
	now := time.Now().UTC().Add(-time.Second)
	record := model.MemoryRecordV2{ID: "MEM-future-essential", ProjectID: w.projectID, Revision: 1, Kind: model.MemoryKindDecision, Lifecycle: model.MemoryDurable, Authority: model.AuthorityOperator, Confidence: model.ConfidenceVerified, Title: "Preservation policy", Body: "Preserve the owner's accepted history.", Scope: "project", ScopeID: w.projectID, ACLScope: lease.owner, ObservedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now, Source: model.MemorySource{Kind: "runtime_service", Reference: "owner"}}
	record.ContentDigest = record.CanonicalDigest()
	if err := w.store.WriteMemoryV2(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	goal := model.GoalContract{ID: "GOAL-native-context", SessionID: w.sessionID, ProjectID: w.runtime.ProjectIdentity(), DesiredOutcome: "Preserve current project history", SuccessCriteria: []string{"Accepted policy is loaded"}, Risk: model.R0, AuthoritySource: "operator"}
	if err := w.store.SaveGoalContract(t.Context(), goal, 0); err != nil {
		t.Fatal(err)
	}
	child := memoryChild(t, w.runtime.ProjectRoot(), &cfg)
	child.call(t, "marshal_project_contract_context", map[string]any{}, false)
	var inventory store.ProjectContractInventoryPage
	if err := json.Unmarshal([]byte(child.call(t, "marshal_project_contract_inventory", map[string]any{"limit": 1}, true)), &inventory); err != nil || len(inventory.Entries) != 1 {
		t.Fatal("exact accepted inventory missing", err)
	}
	e := inventory.Entries[0]
	draft := store.ProjectContractSubmission{Action: "project-contract", Contract: projectcontext.Contract{FormatVersion: 1, Project: w.runtime.ProjectContractBinding(), ID: "ARCH-native-context", Revision: 1, Purpose: "Keep project history reliable", Components: []projectcontext.Component{{ID: "core", Responsibility: "Govern project work", Paths: []string{"src"}}}, Invariants: []string{"Preserve accepted history"}, ReviewedInventoryDigest: inventory.CompletenessDigest, Required: []projectcontext.MemoryRef{{ID: e.ID, ProjectID: e.ProjectID, Revision: e.Revision, Scope: e.Scope, ScopeID: e.ScopeID, ContentDigest: e.ContentDigest, StateFingerprint: e.StateFingerprint, Class: projectcontext.Policy, Applies: projectcontext.Applicability{TaskIDs: []string{"TASK-future"}}, AcceptanceOccurrence: "supported-owner-policy", Reason: "Required history policy"}}}}
	child.call(t, "marshal_project_contract_submit", map[string]any{"submission_id": "complete-context", "proposal": draft}, true)
	ownerAcceptsArchitecture(t, w)
	authorizeTestMemory(t, w, cfg, true) // Host fixture; no native model approval is claimed.
	raw := child.call(t, "marshal_project_contract_context", map[string]any{}, true)
	var got app.ProjectPlanningInspection
	if err := json.Unmarshal([]byte(raw), &got); err != nil || got.Qualified || got.RequiredCount != 1 || got.GoalID != goal.ID || got.GoalRevision != 1 || got.PacketDigest == "" || !strings.Contains(got.Context, record.Body) || !strings.Contains(got.Context, draft.Contract.Purpose) || !strings.Contains(got.Context, goal.DesiredOutcome) {
		t.Fatal("first-goal complete native inspection lost essential data or granted authority", err)
	}
	if len(raw) > 3072 {
		t.Fatal("complete parent projection exceeded its envelope")
	}
	for _, args := range []map[string]any{{"actor": lease.owner}, {"session": w.sessionID}, {"limit": 1}, {"task_id": "TASK-future"}} {
		child.call(t, "marshal_project_contract_context", args, false)
	}
	if _, err := w.store.UpdateMemory(t.Context(), record.ProjectID, record.ID, record.Revision, func(m *model.MemoryRecordV2) error { m.ACLScope = "foreign"; return nil }); err != nil {
		t.Fatal(err)
	}
	child.call(t, "marshal_project_contract_context", map[string]any{}, false)
}
