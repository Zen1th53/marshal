//go:build !governed

package app

import (
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/store"
)

// stuckOwnerAcceptance leaves rt's project in the state a live run was left
// in: the owner accepted revision 1 in the browser for the draft of launch-1,
// the Marshal chat restarted (launch-1 revoked, its draft expired), and the
// restarted chat (launch-2) resubmitted the identical contract, whose allow
// the old launch-bound check refused. Nothing is active.
func stuckOwnerAcceptance(t *testing.T, rt *Runtime) (projectcontext.Contract, string) {
	t.Helper()
	s := rt.Store()
	if err := s.InitMCPProposals(t.Context()); err != nil {
		t.Fatal(err)
	}
	project := rt.ProjectContractBinding()
	audience, err := rt.projectContractOwnerAudience(t.Context(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	_, inventory, err := s.ProjectContractInventory(t.Context(), project, audience)
	if err != nil {
		t.Fatal(err)
	}
	c := projectcontext.Contract{FormatVersion: projectcontext.FormatVersion, Project: project, ID: "marshal-project-architecture", Revision: 1, Purpose: "Plan with the owner", Components: []projectcontext.Component{{ID: "core", Responsibility: "Core", Paths: []string{"src"}}}, Invariants: []string{"Owner accepts in the browser"}, Required: []projectcontext.MemoryRef{}, ReviewedInventoryDigest: inventory}
	sub := store.ProjectContractSubmission{Action: "project-contract", ExpectedVersion: 0, Contract: c}
	if _, _, err = s.AdmitMCPProjectContract(t.Context(), store.MCPProposal{ID: "mcp:first", Launch: "launch-1", Owner: "owner", Provider: "claude", Submission: "first"}, sub); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.RecordProjectContractInstruction(t.Context(), project, "session", "launch-1", "browser:owner-accept", store.BrowserAcceptanceInstruction, time.Now().UTC().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = s.ExpireMCPLaunch(t.Context(), rt.ProjectID(), "launch-1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.AdmitMCPProjectContract(t.Context(), store.MCPProposal{ID: "mcp:again", Launch: "launch-2", Owner: "owner", Provider: "claude", Submission: "again"}, sub); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReadAcceptedProjectContract(t.Context(), project); !errors.Is(err, projectcontext.ErrReviewPending) {
		t.Fatalf("fixture is not stuck: %v", err)
	}
	digest, _ := c.CanonicalDigest()
	return c, digest
}

// An existing project left stuck recovers when the owner runtime opens: the
// owner's browser Accept of exactly that revision and digest is applied, once,
// and recorded as an event.
func TestOwnerAcceptedProjectContractRecoversWhenRuntimeOpens(t *testing.T) {
	root := runtimeRepo(t).Path()
	if _, err := Bootstrap(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	rt, err := Open(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	c, digest := stuckOwnerAcceptance(t, rt)
	if err = rt.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	current, err := reopened.Store().ReadAcceptedProjectContract(t.Context(), reopened.ProjectContractBinding())
	if err != nil || current.Digest != digest || current.Contract.ID != c.ID || current.Version != 1 {
		t.Fatalf("not recovered at open: %+v %v", current, err)
	}
	receipt, decision, err := reopened.Store().ProjectContractInstructionReceipt(t.Context(), reopened.ProjectID(), c.ID, 1)
	if err != nil || receipt.Ref != "browser:owner-accept" || decision != "owner-accept:mcp:again" {
		t.Fatalf("receipt: %+v %q %v", receipt, decision, err)
	}
	events, err := reopened.Store().ListEvents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range events {
		if e.Type == "PROJECT_CONTRACT_OWNER_ACCEPTANCE_APPLIED" {
			n++
			if e.Data["contract_digest"] != digest || e.Data["draft_occurrence"] != "mcp:again" || e.Data["instruction_ref"] != "browser:owner-accept" {
				t.Fatalf("event: %+v", e.Data)
			}
		}
	}
	if n != 1 {
		t.Fatalf("recovery events: %d", n)
	}
	// Opening again applies nothing more.
	if applied, err := reopened.ReconcileOwnerAcceptedProjectContract(t.Context()); err != nil || applied {
		t.Fatalf("second reconcile: %v %v", applied, err)
	}
}

// The Board's architecture status and the Marshal's context read the same
// acceptance: a browser review built from a project contract is accepted
// only when that contract is the accepted project contract.
func TestContractBuiltReviewStatusFollowsAcceptedProjectContract(t *testing.T) {
	rt := browserContextRuntime(t)
	c, digest := stuckOwnerAcceptance(t, rt)
	state := []byte(`{"architecture":{"source_contract":{"id":"` + c.ID + `","revision":1,"digest":"` + digest + `"}}}`)
	status := ArchitectureStatus{Exists: true, Revision: 1, Accepted: true}
	if err := rt.bindArchitectureAcceptance(t.Context(), state, &status); err != nil {
		t.Fatal(err)
	}
	_, contextErr := rt.Store().ReadAcceptedProjectContract(t.Context(), rt.ProjectContractBinding())
	if status.Accepted || !status.OwnerAccepted || !errors.Is(contextErr, projectcontext.ErrReviewPending) {
		t.Fatalf("Board and context disagree before the acceptance applies: %+v %v", status, contextErr)
	}
	if applied, err := rt.ReconcileOwnerAcceptedProjectContract(t.Context()); err != nil || !applied {
		t.Fatalf("reconcile: %v %v", applied, err)
	}
	status = ArchitectureStatus{Exists: true, Revision: 1, Accepted: true}
	if err := rt.bindArchitectureAcceptance(t.Context(), state, &status); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Store().ReadAcceptedProjectContract(t.Context(), rt.ProjectContractBinding()); err != nil || !status.Accepted || status.OwnerAccepted {
		t.Fatalf("Board and context disagree after the acceptance applied: %+v %v", status, err)
	}
	// A review of a different digest is not accepted by that contract.
	other := []byte(`{"architecture":{"source_contract":{"id":"` + c.ID + `","revision":1,"digest":"sha256:other"}}}`)
	status = ArchitectureStatus{Exists: true, Revision: 1, Accepted: true}
	if err := rt.bindArchitectureAcceptance(t.Context(), other, &status); err != nil {
		t.Fatal(err)
	}
	if status.Accepted || !status.OwnerAccepted {
		t.Fatalf("a different digest reads as accepted: %+v", status)
	}
	// A review not built from a contract keeps its own status.
	status = ArchitectureStatus{Exists: true, Revision: 1, Accepted: true}
	if err := rt.bindArchitectureAcceptance(t.Context(), []byte(`{"architecture":{}}`), &status); err != nil || !status.Accepted {
		t.Fatalf("model-drafted review: %+v %v", status, err)
	}
}
