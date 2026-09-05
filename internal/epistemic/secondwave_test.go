package epistemic

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestFailureFingerprintCutRetry(t *testing.T) {
	reg := NewFingerprintRegistry()

	rawErr1 := "2026-09-05T12:00:00Z panic: runtime error at 0xdeadbeef: nil pointer dereference"
	rawErr2 := "2026-09-05T12:05:00Z panic: runtime error at 0x12345678: nil pointer dereference"

	// Attempt 1: First occurrence -> retry allowed
	fp1 := reg.RecordFailure("task-1", rawErr1)
	if fp1.Action != "RETRY_ALLOWED" {
		t.Fatalf("expected RETRY_ALLOWED on 1st occurrence, got %s", fp1.Action)
	}

	shouldCut, reason := reg.ShouldCutRetry(rawErr1)
	if shouldCut {
		t.Fatalf("should not cut retry on 1st occurrence")
	}

	// Attempt 2: Same normalized signature (timestamps & memory addresses normalized away)
	fp2 := reg.RecordFailure("task-1", rawErr2)
	if fp2.Action != "CUT_RETRY_AND_ESCALATE" {
		t.Fatalf("expected CUT_RETRY_AND_ESCALATE on 2nd occurrence, got %s", fp2.Action)
	}
	if fp2.Occurrences != 2 {
		t.Fatalf("expected 2 occurrences, got %d", fp2.Occurrences)
	}

	shouldCut, reason = reg.ShouldCutRetry(rawErr2)
	if !shouldCut {
		t.Fatalf("expected shouldCut to be true on 2nd occurrence")
	}
	if !strings.Contains(reason, "cutting blind retry") {
		t.Fatalf("unexpected cut reason: %s", reason)
	}
}

func TestReviewTheReviewerDiscipline(t *testing.T) {
	auditor := NewReviewAuditor()

	critClaim := model.Claim{
		ID:          "claim-crit",
		Criticality: model.CriticalityBlocker,
	}

	// 1. "LGTM" as evidence must be rejected
	err := auditor.AuditClaimEvidence(critClaim, model.EvidenceRef{
		EvidenceID: "ev-1",
		Summary:    "LGTM",
	})
	if err == nil || !strings.Contains(err.Error(), "LGTM or casual peer agreement is not admissible") {
		t.Fatalf("expected LGTM rejection, got: %v", err)
	}

	// 2. Critical claim without deterministic tool evidence must be rejected
	err = auditor.AuditClaimEvidence(critClaim, model.EvidenceRef{
		EvidenceID:      "ev-2",
		Summary:         "manual review note",
		IsDeterministic: false,
	})
	if err == nil || !strings.Contains(err.Error(), "is non-deterministic") {
		t.Fatalf("expected non-deterministic rejection for critical claim, got: %v", err)
	}

	// 3. Oracle-derived test evidence flagged as verification theater
	err = auditor.AuditClaimEvidence(critClaim, model.EvidenceRef{
		EvidenceID:      "ev-3",
		Summary:         "run test suite",
		IsDeterministic: true,
		Digest:          "sha256:abc12345",
		IsOracleDerived: true,
	})
	if err == nil || !strings.Contains(err.Error(), "derived from the test oracle itself") {
		t.Fatalf("expected verification theater detection, got: %v", err)
	}

	// 4. Valid deterministic evidence passes
	err = auditor.AuditClaimEvidence(critClaim, model.EvidenceRef{
		EvidenceID:      "ev-4",
		Tool:            "go test -v ./...",
		Summary:         "All 42 tests passed with zero failures",
		IsDeterministic: true,
		Digest:          "sha256:fedcba9876",
		IsOracleDerived: false,
	})
	if err != nil {
		t.Fatalf("expected valid evidence to pass audit, got: %v", err)
	}
}

func TestMutationCheckTheaterDetection(t *testing.T) {
	auditor := NewReviewAuditor()

	// If a test suite passes when a mutant is injected, it's insensitive verification theater
	err := auditor.AuditMutationSensitivity(true)
	if err == nil || !strings.Contains(err.Error(), "verification theater detected") {
		t.Fatalf("expected theater error on mutant pass, got: %v", err)
	}

	// When mutant causes test failure, the test is sensitive and valid
	err = auditor.AuditMutationSensitivity(false)
	if err != nil {
		t.Fatalf("expected nil error when test fails mutant, got: %v", err)
	}
}

func TestPostMortemCardGeneration(t *testing.T) {
	toks := int64(45000)
	cost := 0.15
	card := PostMortemCard{
		GoalID:          "goal-v15",
		GoalRevision:    3,
		WhatWorked:      []string{"TUI workspace rendered one-screen dashboard", "CAS concurrency prevented conflict"},
		WhatWasRedone:   []string{"Refactored exitCode nil check"},
		WrongAssumptions: []string{"Assumed exitCode(nil) returned 0"},
		Failures:        []string{"Fingerprint fp-123 caused retry cut"},
		ConsumedBudget: model.ConsumedBudget{
			TotalTokens: &toks,
			CostUSD:     &cost,
			ModelCalls:  12,
			Handoffs:    3,
			Duration:    2 * time.Minute,
		},
		UnresolvedRisks: []string{"Real provider credentials not installed on local CI"},
		RoutingLessons:  []string{"Codex best suited for developer tasks, Claude for architecture"},
		GeneratedAt:     time.Now().UTC(),
	}

	md := GeneratePostMortemCard(card)
	if !strings.Contains(md, "# Post-Mortem Card: Goal goal-v15 (rev 3)") {
		t.Fatalf("expected title in postmortem:\n%s", md)
	}
	if !strings.Contains(md, "45000 tokens") || !strings.Contains(md, "$0.1500") {
		t.Fatalf("expected budget details in postmortem:\n%s", md)
	}
	if !strings.Contains(md, "Refactored exitCode nil check") {
		t.Fatalf("expected what was redone in postmortem:\n%s", md)
	}
}
