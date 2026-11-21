//go:build !governed

package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// admitLaunchContract admits a project-contract draft on launch, as a Marshal
// chat submits it, and returns its occurrence.
func admitLaunchContract(t *testing.T, s *Store, c projectcontext.Contract, launch, suffix string) MCPProposal {
	t.Helper()
	source, _, err := s.AdmitMCPProjectContract(t.Context(), MCPProposal{ID: "mcp:contract-" + suffix, Launch: launch, Owner: "owner", Provider: "codex", Submission: "contract-" + suffix}, ProjectContractSubmission{Action: "project-contract", ExpectedVersion: c.Revision - 1, Contract: c})
	if err != nil {
		t.Fatal(err)
	}
	return source
}

// admitLaunchDecision admits the Marshal's decide answer for source on its
// launch and brings it to applying, as the runtime does before the effect.
func admitLaunchDecision(t *testing.T, s *Store, c projectcontext.Contract, source MCPProposal, allow bool, suffix string) permission.Request {
	t.Helper()
	digest, err := c.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	binding, _ := json.Marshal(ProjectContractApprovalBinding{source.Digest, digest, c.ReviewedInventoryDigest, c.Revision - 1})
	req := permission.Request{Kind: "project-contract", Object: c.ID, Scope: c.Project.StorageID, Who: "Marshal", Reason: "Reviewed project contract", ProposalID: source.ID, ApprovalBinding: string(binding), SourceLaunch: source.Launch, SourceOccurrence: "mcp:decision-" + suffix}
	raw, _ := json.Marshal(map[string]any{"action": "decide", "id": "QUESTION-" + suffix, "allow": allow, "reason": "The owner accepted exactly this contract in the browser"})
	if _, _, err = s.AdmitMCPProposal(t.Context(), req.Scope, MCPProposal{ID: req.SourceOccurrence, Launch: source.Launch, Owner: "owner", Provider: "codex", Submission: "decision-" + suffix, Payload: string(raw)}); err != nil {
		t.Fatal(err)
	}
	requestJSON, _ := json.Marshal(req)
	if err = s.AcceptMCPDecision(t.Context(), req.Scope, req.SourceOccurrence, string(requestJSON), req.Key()); err != nil {
		t.Fatal(err)
	}
	if err = s.SetMCPProposalState(t.Context(), req.Scope, req.SourceOccurrence, "applying", "protected test effect entered"); err != nil {
		t.Fatal(err)
	}
	return req
}

// The live sequence: submit, owner Accept in the browser, the Marshal chat
// restarts under a new launch, it resubmits the identical contract and allows
// it. The owner's Accept of that exact revision and digest covers the copy.
func TestProjectContractOwnerAcceptCoversIdenticalResubmissionAfterChatRestart(t *testing.T) {
	s := projectStore(t)
	if err := s.InitMCPProposals(t.Context()); err != nil {
		t.Fatal(err)
	}
	c, a, now := contextStoreDraft(t, s, nil)
	digest, _ := c.CanonicalDigest()
	first := admitLaunchContract(t, s, c, "launch-1", "first")
	i, fresh, err := s.RecordProjectContractInstruction(t.Context(), c.Project, "chat", "launch-1", "browser:accept", BrowserAcceptanceInstruction, time.Now().UTC().Add(time.Second))
	if err != nil || !fresh || i.ProposalOccurrence != first.ID || i.ContractDigest != digest {
		t.Fatalf("owner accept: %+v %v %v", i, fresh, err)
	}
	// The chat restarts: its launch is revoked and the draft expires.
	if err = s.ExpireMCPLaunch(t.Context(), c.Project.StorageID, "launch-1"); err != nil {
		t.Fatal(err)
	}
	again := admitLaunchContract(t, s, c, "launch-2", "again")
	// The identical copy does not ask the owner again.
	if p, found, err := s.ReadPendingProjectContract(t.Context(), c.Project, "launch-2"); err != nil || found {
		t.Fatalf("identical resubmission waits for a second owner Accept: %+v %v %v", p, found, err)
	}
	req := admitLaunchDecision(t, s, c, again, true, "again")
	accepted, err := s.CommitMCPProjectContractDecisionOnInstruction(t.Context(), req, true, a, now, contextAudit(req, now))
	if err != nil || accepted.ID != c.ID {
		t.Fatalf("allow of the identical resubmission: %+v %v", accepted, err)
	}
	current, err := s.ReadAcceptedProjectContract(t.Context(), c.Project)
	if err != nil || current.Digest != digest || current.Version != 1 {
		t.Fatalf("accepted: %+v %v", current, err)
	}
	if receipt, decision, err := s.ProjectContractInstructionReceipt(t.Context(), c.Project.StorageID, c.ID, c.Revision); err != nil || receipt.Ref != "browser:accept" || decision != req.SourceOccurrence {
		t.Fatalf("receipt: %+v %s %v", receipt, decision, err)
	}
	// Nothing is left for a reconcile to apply.
	if _, found, err := s.ReadOwnerAcceptedProjectContract(t.Context(), c.Project); err != nil || found {
		t.Fatalf("consumed acceptance still waits: %v %v", found, err)
	}
}

// A contract whose digest differs from the one the owner accepted is never
// covered: not by the Marshal's allow, not by a reconcile, and it waits for
// the owner's own Accept.
func TestProjectContractOwnerAcceptNeverCoversADifferentDigest(t *testing.T) {
	s := projectStore(t)
	if err := s.InitMCPProposals(t.Context()); err != nil {
		t.Fatal(err)
	}
	c, a, now := contextStoreDraft(t, s, nil)
	admitLaunchContract(t, s, c, "launch-1", "first")
	if _, _, err := s.RecordProjectContractInstruction(t.Context(), c.Project, "chat", "launch-1", "browser:accept", BrowserAcceptanceInstruction, time.Now().UTC().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.ExpireMCPLaunch(t.Context(), c.Project.StorageID, "launch-1"); err != nil {
		t.Fatal(err)
	}
	other := c
	other.Purpose = "Something the owner did not see"
	changed := admitLaunchContract(t, s, other, "launch-2", "changed")
	if p, found, err := s.ReadPendingProjectContract(t.Context(), c.Project, "launch-2"); err != nil || !found || p.Occurrence != changed.ID {
		t.Fatalf("a changed contract must wait for the owner: %+v %v %v", p, found, err)
	}
	req := admitLaunchDecision(t, s, other, changed, true, "changed")
	if _, err := s.CommitMCPProjectContractDecisionOnInstruction(t.Context(), req, true, a, now, contextAudit(req, now)); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("allow of a different digest: %v", err)
	}
	// The newest draft is not what the owner accepted: no reconcile.
	if _, applied, err := s.ApplyOwnerAcceptedProjectContract(t.Context(), c.Project, func(_ context.Context, owner string) (ProjectContextAudience, error) {
		a.Actor = owner
		return a, nil
	}, now, ""); err != nil || applied {
		t.Fatalf("reconcile applied a contract the owner did not accept: %v %v", applied, err)
	}
	if _, err := s.ReadAcceptedProjectContract(t.Context(), c.Project); !errors.Is(err, projectcontext.ErrReviewPending) {
		t.Fatalf("a different digest became accepted: %v", err)
	}
}

// The stuck state of an existing project: the owner accepted in the browser,
// the identical resubmission's allow was refused (launch-bound), and nothing
// is active. The reconcile at open applies the owner's acceptance exactly
// once and records it as an event.
func TestProjectContractOwnerAcceptStuckStateRecoversOnReconcile(t *testing.T) {
	s := projectStore(t)
	if err := s.InitMCPProposals(t.Context()); err != nil {
		t.Fatal(err)
	}
	c, a, now := contextStoreDraft(t, s, nil)
	digest, _ := c.CanonicalDigest()
	admitLaunchContract(t, s, c, "launch-1", "first")
	i, _, err := s.RecordProjectContractInstruction(t.Context(), c.Project, "chat", "launch-1", "browser:accept", BrowserAcceptanceInstruction, time.Now().UTC().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ExpireMCPLaunch(t.Context(), c.Project.StorageID, "launch-1"); err != nil {
		t.Fatal(err)
	}
	again := admitLaunchContract(t, s, c, "launch-2", "again")
	audience := func(_ context.Context, owner string) (ProjectContextAudience, error) {
		a.Actor = owner
		return a, nil
	}
	seen, found, err := s.ReadOwnerAcceptedProjectContract(t.Context(), c.Project)
	if err != nil || !found || seen.Draft.ID != again.ID || seen.Instruction.Ref != i.Ref || seen.Digest != digest {
		t.Fatalf("stuck acceptance: %+v %v %v", seen, found, err)
	}
	got, applied, err := s.ApplyOwnerAcceptedProjectContract(t.Context(), c.Project, audience, now, "test reconcile")
	if err != nil || !applied || got.Draft.ID != again.ID {
		t.Fatalf("reconcile: %+v %v %v", got, applied, err)
	}
	current, err := s.ReadAcceptedProjectContract(t.Context(), c.Project)
	if err != nil || current.Digest != digest || current.Version != 1 {
		t.Fatalf("accepted after reconcile: %+v %v", current, err)
	}
	if receipt, decision, err := s.ProjectContractInstructionReceipt(t.Context(), c.Project.StorageID, c.ID, c.Revision); err != nil || receipt.Ref != "browser:accept" || decision != "owner-accept:"+again.ID {
		t.Fatalf("receipt: %+v %s %v", receipt, decision, err)
	}
	if p, err := s.MCPProposal(t.Context(), c.Project.StorageID, again.ID); err != nil || p.State != "committed" {
		t.Fatalf("draft state: %+v %v", p, err)
	}
	var events int
	if err = s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM audit_events WHERE event_type='PROJECT_CONTRACT_OWNER_ACCEPTANCE_APPLIED'`).Scan(&events); err != nil || events != 1 {
		t.Fatalf("event: %d %v", events, err)
	}
	// Idempotent: a second open finds nothing.
	if _, applied, err = s.ApplyOwnerAcceptedProjectContract(t.Context(), c.Project, audience, now, ""); err != nil || applied {
		t.Fatalf("second reconcile: %v %v", applied, err)
	}
}

// Only a browser Accept authorizes the reconcile: a recorded chat
// instruction for the same draft is left to the Marshal's allow.
func TestProjectContractReconcileNeedsBrowserAccept(t *testing.T) {
	s := projectStore(t)
	if err := s.InitMCPProposals(t.Context()); err != nil {
		t.Fatal(err)
	}
	c, a, now := contextStoreDraft(t, s, nil)
	admitLaunchContract(t, s, c, "launch-1", "first")
	if _, _, err := s.RecordProjectContractInstruction(t.Context(), c.Project, "chat", "launch-1", "operator:chat", BrowserAcceptanceInstruction, time.Now().UTC().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, applied, err := s.ApplyOwnerAcceptedProjectContract(t.Context(), c.Project, func(_ context.Context, owner string) (ProjectContextAudience, error) {
		a.Actor = owner
		return a, nil
	}, now, ""); err != nil || applied {
		t.Fatalf("a non-browser instruction reconciled: %v %v", applied, err)
	}
	if _, err := s.ReadAcceptedProjectContract(t.Context(), c.Project); !errors.Is(err, projectcontext.ErrReviewPending) {
		t.Fatalf("accepted without a browser Accept: %v", err)
	}
}

// Only a browser Accept crosses launches: a chat instruction stays bound to
// the launch whose draft it was recorded for.
func TestProjectContractChatInstructionDoesNotCrossLaunches(t *testing.T) {
	s := projectStore(t)
	if err := s.InitMCPProposals(t.Context()); err != nil {
		t.Fatal(err)
	}
	c, a, now := contextStoreDraft(t, s, nil)
	admitLaunchContract(t, s, c, "launch-1", "first")
	if _, _, err := s.RecordProjectContractInstruction(t.Context(), c.Project, "chat", "launch-1", "operator:chat", BrowserAcceptanceInstruction, time.Now().UTC().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.ExpireMCPLaunch(t.Context(), c.Project.StorageID, "launch-1"); err != nil {
		t.Fatal(err)
	}
	again := admitLaunchContract(t, s, c, "launch-2", "again")
	if _, found, err := s.ReadPendingProjectContract(t.Context(), c.Project, "launch-2"); err != nil || !found {
		t.Fatalf("a chat instruction of another launch hid the owner gate: %v %v", found, err)
	}
	req := admitLaunchDecision(t, s, c, again, true, "again")
	if _, err := s.CommitMCPProjectContractDecisionOnInstruction(t.Context(), req, true, a, now, contextAudit(req, now)); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("a chat instruction crossed launches: %v", err)
	}
}

// Many newer unrelated proposals do not hide the accepted draft from the
// open-time reconcile.
func TestProjectContractOwnerAcceptFoundBehindManyOtherProposals(t *testing.T) {
	s := projectStore(t)
	if err := s.InitMCPProposals(t.Context()); err != nil {
		t.Fatal(err)
	}
	c, _, _ := contextStoreDraft(t, s, nil)
	admitLaunchContract(t, s, c, "launch-1", "first")
	if _, _, err := s.RecordProjectContractInstruction(t.Context(), c.Project, "chat", "launch-1", "browser:accept", BrowserAcceptanceInstruction, time.Now().UTC().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.ExpireMCPLaunch(t.Context(), c.Project.StorageID, "launch-1"); err != nil {
		t.Fatal(err)
	}
	again := admitLaunchContract(t, s, c, "launch-2", "again")
	for n := 0; n < 300; n++ {
		raw, _ := json.Marshal(map[string]any{"action": "decide", "id": fmt.Sprintf("QUESTION-%d", n), "allow": false, "reason": "unrelated"})
		if _, _, err := s.AdmitMCPProposal(t.Context(), c.Project.StorageID, MCPProposal{ID: fmt.Sprintf("mcp:other-%d", n), Launch: "launch-2", Owner: "owner", Provider: "codex", Submission: fmt.Sprintf("other-%d", n), Payload: string(raw)}); err != nil {
			t.Fatal(err)
		}
	}
	seen, found, err := s.ReadOwnerAcceptedProjectContract(t.Context(), c.Project)
	if err != nil || !found || seen.Draft.ID != again.ID {
		t.Fatalf("accepted draft behind 300 proposals: %+v %v %v", seen, found, err)
	}
}
