//go:build !governed

package store

import (
	"testing"
	"time"
)

// The Board's owner gate reads the draft an "accept architecture" would
// bind to now: pending after the Marshal submits it, gone once the owner
// instructed or the contract was accepted, never for a stale resubmission.
func TestReadPendingProjectContractFollowsTheOwnerGate(t *testing.T) {
	s := projectStore(t)
	ctx := t.Context()
	c, a, now := contextStoreDraft(t, s, nil)
	if _, found, err := s.ReadPendingProjectContract(ctx, c.Project, "launch"); err != nil || found {
		t.Fatalf("no proposal tables yet: found=%v err=%v", found, err)
	}
	if err := s.InitMCPProposals(ctx); err != nil {
		t.Fatal(err)
	}
	if _, found, err := s.ReadPendingProjectContract(ctx, c.Project, "launch"); err != nil || found {
		t.Fatalf("nothing submitted: found=%v err=%v", found, err)
	}
	req := admitContextDecision(t, s, c, true, "owner")
	p, found, err := s.ReadPendingProjectContract(ctx, c.Project, "launch")
	digest, _ := c.CanonicalDigest()
	if err != nil || !found || p.ID != c.ID || p.Revision != c.Revision || p.Digest != digest || p.Purpose != c.Purpose || p.Occurrence != req.ProposalID || p.Submitted.IsZero() {
		t.Fatalf("pending contract: %+v found=%v err=%v", p, found, err)
	}
	if _, found, _ := s.ReadPendingProjectContract(ctx, c.Project, "other-launch"); found {
		t.Fatal("a draft of another launch was pending for this one")
	}
	if _, found, _ := s.ReadPendingProjectContract(ctx, c.Project, ""); !found {
		t.Fatal("without a launch filter the draft was not found")
	}
	if _, _, err := s.RecordProjectContractInstruction(ctx, c.Project, "chat", "launch", "operator:owner", "accept architecture", time.Now().UTC().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := s.ReadPendingProjectContract(ctx, c.Project, "launch"); found {
		t.Fatal("the owner already accepted; the gate stayed")
	}
	if _, err := s.CommitMCPProjectContractDecisionOnInstruction(ctx, req, true, a, now, contextAudit(req, now)); err != nil {
		t.Fatal(err)
	}
	// The walk's case: an identical resubmission drafted against the
	// version before acceptance is not a decision for the owner.
	admitContextDecision(t, s, c, true, "stale")
	if p, found, _ := s.ReadPendingProjectContract(ctx, c.Project, "launch"); found {
		t.Fatalf("stale resubmission is pending: %+v", p)
	}
}
