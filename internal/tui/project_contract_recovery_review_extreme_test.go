//go:build !governed

package tui

import (
	"encoding/json"
	"testing"

	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestIndependentProjectContractRecoveryPreservesExactRequestAndCurrentLaunch(t *testing.T) {
	w, _, cfg := liveMCPFixture(t)
	child := memoryChild(t, w.runtime.ProjectRoot(), &cfg)
	var inventory store.ProjectContractInventoryPage
	if err := json.Unmarshal([]byte(child.call(t, "marshal_project_contract_inventory", map[string]any{"limit": 1}, true)), &inventory); err != nil {
		t.Fatal(err)
	}
	draft := store.ProjectContractSubmission{Action: "project-contract", Contract: projectcontext.Contract{FormatVersion: 1, Project: w.runtime.ProjectContractBinding(), ID: "ARCH-recovery", Revision: 1, Purpose: "Preserve exact authenticated reviewed architecture", Components: []projectcontext.Component{{ID: "core", Responsibility: "Govern exact accepted work", Paths: []string{"src"}}}, Invariants: []string{"No accepted policy may be silently omitted"}, ReviewedInventoryDigest: inventory.CompletenessDigest}}
	child.call(t, "marshal_project_contract_submit", map[string]any{"submission_id": "independent-recovery", "proposal": draft}, true)
	initial := w.permissions.queue.Take(false)
	if len(initial) != 1 {
		t.Fatal("draft did not queue one actual decision", initial)
	}
	// Simulate reconstructing an empty permission scheduler while the native
	// lease remains current. Taking a queue batch alone leaves it outstanding.
	w.permissions.mu.Lock()
	w.permissions.outstanding = nil
	w.permissions.mu.Unlock()
	records, err := w.store.PendingMarshalProposals(t.Context(), w.projectID)
	if err != nil || len(records) != 1 {
		t.Fatal("draft exact source unavailable for recovery", records, err)
	}
	for range 5 {
		w.recoverMarshalProposals()
	}
	recovered := w.permissions.queue.Take(false)
	if len(recovered) != 1 || recovered[0] != initial[0] || recovered[0].Who != "Marshal" || recovered[0].SourceLaunch != w.proposalMCP.active.id {
		t.Fatal("recovery duplicated or rewrote exact decision binding", recovered, initial)
	}
	if _, err = w.store.ReadAcceptedProjectContract(t.Context(), draft.Contract.Project); err != projectcontext.ErrReviewPending {
		t.Fatal("recovery activated unaccepted architecture", err)
	}
	forged := records[0]
	var request permission.Request
	if err = json.Unmarshal([]byte(forged.RequestJSON), &request); err != nil {
		t.Fatal(err)
	}
	request.Reason = "Different draft permission"
	raw, _ := json.Marshal(request)
	forged.RequestJSON = string(raw)
	if !w.recoverBoundMCPMemory(forged) || !w.permissions.queue.Empty() {
		t.Fatal("altered exact request recovered as new authority")
	}
	forged = records[0]
	forged.Line += " "
	if w.recoverBoundMCPMemory(forged) || !w.permissions.queue.Empty() {
		t.Fatal("altered source recovered through authenticated contract seam")
	}
	// A new live native launch cannot recover the old launch's pending draft.
	bindWorkspaceMCP(t, w)
	if w.recoverBoundMCPMemory(records[0]) {
		t.Fatal("replaced native launch reaccepted old draft source")
	}
	w.recoverMarshalProposals()
	if !w.permissions.queue.Empty() {
		t.Fatal("old launch contract draft requeued after replacement")
	}
}
