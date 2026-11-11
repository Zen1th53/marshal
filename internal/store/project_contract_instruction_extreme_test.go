//go:build !governed

package store

import (
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

func TestProjectContractOwnerInstructionLetsMarshalAcceptExactDraft(t *testing.T) {
	s := projectStore(t)
	if err := s.InitMCPProposals(t.Context()); err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC()
	c, a, now := contextStoreDraft(t, s, nil)
	// An owner message older than the draft is not an instruction for it.
	if _, _, err := s.RecordProjectContractInstruction(t.Context(), c.Project, "chat", "launch", "operator:early", "accept architecture", before.Add(-time.Minute)); !errors.Is(err, ErrNoPendingProjectContract) {
		t.Fatalf("instruction without a draft: %v", err)
	}
	req := admitContextDecision(t, s, c, true, "owner")
	if _, _, err := s.RecordProjectContractInstruction(t.Context(), c.Project, "chat", "launch", "operator:early", "accept architecture", before); !errors.Is(err, ErrNoPendingProjectContract) {
		t.Fatalf("message predating the draft was bound: %v", err)
	}
	// The Marshal's allow is refused without the owner's recorded instruction.
	if _, err := s.CommitMCPProjectContractDecisionOnInstruction(t.Context(), req, true, a, now, contextAudit(req, now)); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("allow without instruction: %v", err)
	}
	if _, err := s.ReadAcceptedProjectContract(t.Context(), c.Project); !errors.Is(err, projectcontext.ErrReviewPending) {
		t.Fatalf("refused allow left acceptance: %v", err)
	}
	uzbek := "Boshla. Arxitekturani qabul qil va Goal'ni rasmiylashtir — Marshal'da bunga ruxsat beraman"
	for _, text := range []string{uzbek, "please accept architecture", "don't accept architecture", "Accept Architecture", " accept architecture ", "approve architecture", "accept the architecture", "approve the architecture", "/marshal accept-architecture"} {
		if _, _, err := s.RecordProjectContractInstruction(t.Context(), c.Project, "chat", "launch", "operator:other", text, time.Now().UTC()); !errors.Is(err, model.ErrPolicyDenied) {
			t.Fatalf("%q recorded: %v", text, err)
		}
	}
	if _, _, err := s.RecordProjectContractInstruction(t.Context(), c.Project, "chat", "foreign-launch", "operator:foreign", "accept architecture", time.Now().UTC()); !errors.Is(err, ErrNoPendingProjectContract) {
		t.Fatalf("instruction crossed launches: %v", err)
	}
	at := time.Now().UTC().Add(time.Second)
	i, fresh, err := s.RecordProjectContractInstruction(t.Context(), c.Project, "chat", "launch", "operator:owner", BrowserAcceptanceInstruction, at)
	if err != nil || !fresh {
		t.Fatalf("record: %v %v", fresh, err)
	}
	digest, _ := c.CanonicalDigest()
	if i.ContractID != c.ID || i.Revision != c.Revision || i.ContractDigest != digest || i.ProposalOccurrence != req.ProposalID || i.Session != "chat" {
		t.Fatalf("binding: %+v", i)
	}
	if _, fresh, err = s.RecordProjectContractInstruction(t.Context(), c.Project, "chat", "launch", "operator:owner", BrowserAcceptanceInstruction, at); err != nil || fresh {
		t.Fatalf("replay must be idempotent and not fresh: %v %v", fresh, err)
	}
	if _, err = s.db.ExecContext(t.Context(), `UPDATE extreme_project_contract_instructions SET contract_digest='forged'`); err == nil {
		t.Fatal("instruction is mutable")
	}
	accepted, err := s.CommitMCPProjectContractDecisionOnInstruction(t.Context(), req, true, a, now, contextAudit(req, now))
	if err != nil || accepted.ID != c.ID {
		t.Fatalf("accept on instruction: %+v %v", accepted, err)
	}
	current, err := s.ReadAcceptedProjectContract(t.Context(), c.Project)
	if err != nil || current.Digest != digest || current.Version != 1 {
		t.Fatalf("accepted contract: %+v %v", current, err)
	}
	receipt, decision, err := s.ProjectContractInstructionReceipt(t.Context(), c.Project.StorageID, c.ID, c.Revision)
	if err != nil || receipt.Ref != "operator:owner" || decision != req.SourceOccurrence {
		t.Fatalf("receipt: %+v %s %v", receipt, decision, err)
	}
	// The accepted draft can no longer bind a new instruction.
	if _, _, err = s.RecordProjectContractInstruction(t.Context(), c.Project, "chat", "launch", "operator:late", "accept architecture", time.Now().UTC().Add(time.Minute)); !errors.Is(err, ErrNoPendingProjectContract) {
		t.Fatalf("accepted draft rebound: %v", err)
	}
}

func TestProjectContractInstructionSurvivesDeniedDecisionForIdenticalResubmission(t *testing.T) {
	s := projectStore(t)
	if err := s.InitMCPProposals(t.Context()); err != nil {
		t.Fatal(err)
	}
	c, a, now := contextStoreDraft(t, s, nil)
	denied := admitContextDecision(t, s, c, false, "first")
	if _, err := s.CommitMCPProjectContractDecisionOnInstruction(t.Context(), denied, false, a, now, contextAudit(denied, now)); err != nil {
		t.Fatalf("deny needs no instruction: %v", err)
	}
	if _, _, err := s.RecordProjectContractInstruction(t.Context(), c.Project, "chat", "launch", "operator:after-deny", "accept architecture", time.Now().UTC().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	// A different contract is not covered by the instruction.
	other := c
	other.Purpose = "Something the owner did not see"
	otherReq := admitContextDecision(t, s, other, true, "other")
	if _, err := s.CommitMCPProjectContractDecisionOnInstruction(t.Context(), otherReq, true, a, now, contextAudit(otherReq, now)); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("instruction covered a different contract: %v", err)
	}
	again := admitContextDecision(t, s, c, true, "again")
	if _, err := s.CommitMCPProjectContractDecisionOnInstruction(t.Context(), again, true, a, now, contextAudit(again, now)); err != nil {
		t.Fatalf("identical resubmission: %v", err)
	}
	if _, err := s.ReadAcceptedProjectContract(t.Context(), c.Project); err != nil {
		t.Fatal(err)
	}
}
