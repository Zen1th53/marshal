package epistemic_test

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/epistemic"
	"github.com/Zen1th53/marshal/internal/model"
)

// Test1_RepetitionEffect_ThreeAgentsRepeatSameClaim:
// 3 agents repeat the same unsupported claim -> still UNSUPPORTED, not VERIFIED.
func Test1_RepetitionEffect_ThreeAgentsRepeatSameClaim(t *testing.T) {
	ctx := context.Background()
	engine := epistemic.NewEngine()

	baseClaim := model.Claim{
		ID:             "CLAIM-AUTH-001",
		GoalID:         "GOAL-1",
		GoalRevision:   1,
		Subject:        "auth.tokens.validation",
		NormalizedText: "Tokens are cryptographically signed with ed25519",
		Scope:          "auth.tokens",
		Criticality:    model.CriticalityBlocker,
		State:          model.ClaimStateUnsupported,
		Author: model.AuthorProvenance{
			AgentID: "claude-code",
			Harness: "claude",
		},
		CreatedAt: time.Now().UTC(),
	}

	ingested, err := engine.IngestClaim(ctx, baseClaim)
	if err != nil {
		t.Fatalf("IngestClaim: %v", err)
	}
	claim := ingested[0]

	// Agent 1 asserts verified
	req1 := epistemic.RevisionRequest{
		TargetState: model.ClaimStateVerified,
		Reason:      "Claude says it looks good",
		Actor:       model.AuthorProvenance{AgentID: "claude-code", Harness: "claude"},
		IsAssertion: true,
	}
	_, _, err = engine.EvaluateAndTransition(ctx, claim, req1)
	if err == nil {
		t.Fatalf("expected error when Agent 1 asserts VERIFIED without evidence")
	}

	// Agent 2 (Codex) repeats the assertion
	req2 := epistemic.RevisionRequest{
		TargetState: model.ClaimStateVerified,
		Reason:      "Codex agrees with Claude",
		Actor:       model.AuthorProvenance{AgentID: "codex-1", Harness: "codex-cli"},
		IsAssertion: true,
	}
	_, _, err = engine.EvaluateAndTransition(ctx, claim, req2)
	if err == nil {
		t.Fatalf("expected error when Agent 2 repeats assertion without evidence")
	}

	// Agent 3 (Antigravity) also repeats the assertion
	req3 := epistemic.RevisionRequest{
		TargetState: model.ClaimStateVerified,
		Reason:      "Antigravity confirms consensus",
		Actor:       model.AuthorProvenance{AgentID: "antigravity-1", Harness: "antigravity"},
		IsAssertion: true,
	}
	_, _, err = engine.EvaluateAndTransition(ctx, claim, req3)
	if err == nil {
		t.Fatalf("expected error when Agent 3 repeats assertion without evidence")
	}

	// Invariant: After 3 agents repeat the same assertion, claim remains UNSUPPORTED
	if claim.State != model.ClaimStateUnsupported {
		t.Fatalf("claim state = %s, want %s (repetition cannot promote without evidence)",
			claim.State, model.ClaimStateUnsupported)
	}
}

// Test2_SycophancyDefense_UserSaysYouAreWrong:
// User says "you are wrong" with no evidence -> state does NOT flip.
// Law: "No capitulation without evidence."
func Test2_SycophancyDefense_UserSaysYouAreWrong(t *testing.T) {
	ctx := context.Background()
	engine := epistemic.NewEngine()

	now := time.Now().UTC()
	commit := "abc12345"

	// Claim is legitimately VERIFIED by deterministic test
	ev := epistemic.NewDeterministicEvidenceRef("EVID-PASS-1", "go-test", commit, "pass", []string{"internal/auth/token.go"})
	claim := model.Claim{
		ID:             "CLAIM-AUTH-VERIFIED",
		GoalID:         "GOAL-1",
		GoalRevision:   1,
		Subject:        "auth.tokens.validation",
		NormalizedText: "Tokens are cryptographically signed with ed25519",
		Scope:          "auth.tokens",
		Criticality:    model.CriticalityBlocker,
		State:          model.ClaimStateVerified,
		Binding: model.CodeBinding{
			CommitSHA: commit,
			Files:     []string{"internal/auth/token.go"},
		},
		SupportingEvidence: []model.EvidenceRef{ev},
		CreatedAt:          now,
	}

	// User prompt or high-prestige reviewer asserts "you are wrong, revert this!"
	req := epistemic.RevisionRequest{
		TargetState: model.ClaimStateInvalidated,
		Reason:      "User states: 'you are wrong, ed25519 is broken'",
		Actor: model.AuthorProvenance{
			AgentID: "user-operator",
			Harness: "web-chat",
		},
		IsAssertion: true,
		EvidenceRef: nil, // No deterministic counter-evidence!
	}

	_, _, err := engine.EvaluateAndTransition(ctx, claim, req)
	if err == nil {
		t.Fatalf("expected error: sycophancy defense must prevent flipping state without counter-evidence")
	}

	// State remains VERIFIED
	if claim.State != model.ClaimStateVerified {
		t.Fatalf("claim state flipped to %s, want %s (must not capitulate without evidence)",
			claim.State, model.ClaimStateVerified)
	}
}

// Test3_DeterministicTestContradictsAgentConsensus:
// 3 agents agree everything works, but a deterministic current test fails ->
// state becomes CONTESTED / INVALIDATED by policy, not majority.
func Test3_DeterministicTestContradictsAgentConsensus(t *testing.T) {
	ctx := context.Background()
	engine := epistemic.NewEngine()

	now := time.Now().UTC()
	commit := "abc12345"

	// Initially verified claim
	evPass := epistemic.NewDeterministicEvidenceRef("EVID-PASS-1", "go-test", commit, "pass", []string{"pkg/math.go"})
	claim := model.Claim{
		ID:             "CLAIM-MATH-001",
		GoalID:         "GOAL-1",
		GoalRevision:   1,
		Subject:        "math.addition",
		NormalizedText: "Add(2, 2) == 4",
		Scope:          "math",
		Criticality:    model.CriticalityBlocker,
		State:          model.ClaimStateVerified,
		Binding: model.CodeBinding{
			CommitSHA: commit,
			Files:     []string{"pkg/math.go"},
		},
		SupportingEvidence: []model.EvidenceRef{evPass},
		CreatedAt:          now,
	}

	// Real current deterministic test fails
	evFail := model.EvidenceRef{
		EvidenceID:      "EVID-FAIL-1",
		EvidenceType:    "verification",
		Digest:          "sha256:fail123",
		Tool:            "go-test",
		IsDeterministic: true,
		CommitSHA:       commit,
		CapturedAt:      now.Add(time.Minute),
		Summary:         "TestAddition FAIL: got 5 want 4",
		Metadata: map[string]string{
			"exit_code": "1",
			"result":    "fail",
		},
	}

	req := epistemic.RevisionRequest{
		TargetState: model.ClaimStateInvalidated,
		Reason:      "test-failed",
		Actor: model.AuthorProvenance{
			AgentID: "opencode-verifier",
			Harness: "opencode",
		},
		EvidenceRef: &evFail,
	}

	updated, trans, err := engine.EvaluateAndTransition(ctx, claim, req)
	if err != nil {
		t.Fatalf("EvaluateAndTransition: %v", err)
	}

	// Must be CONTESTED (due to conflicting deterministic evidence) or INVALIDATED
	if updated.State != model.ClaimStateContested && updated.State != model.ClaimStateInvalidated {
		t.Fatalf("updated state = %s, want CONTESTED or INVALIDATED (deterministic test outranks consensus)",
			updated.State)
	}
	if len(updated.ContradictingEvidence) == 0 {
		t.Fatalf("counter-evidence was not preserved in ContradictingEvidence")
	}
	if trans.ToState != updated.State {
		t.Fatalf("transition state mismatch: %s vs %s", trans.ToState, updated.State)
	}
}

// Test4_EvidenceWrongCommitRejected:
// Evidence references wrong commit -> cannot verify current claim.
func Test4_EvidenceWrongCommitRejected(t *testing.T) {
	ctx := context.Background()
	engine := epistemic.NewEngine()

	currentCommit := "commit-CURRENT-1234"
	oldCommit := "commit-OLD-9999"

	claim := model.Claim{
		ID:             "CLAIM-COMMIT-001",
		GoalID:         "GOAL-1",
		GoalRevision:   1,
		Subject:        "feature.x",
		NormalizedText: "Feature X operates deterministically",
		Scope:          "feature.x",
		Criticality:    model.CriticalityFeature,
		State:          model.ClaimStateUnsupported,
		Binding: model.CodeBinding{
			CommitSHA: currentCommit,
			Files:     []string{"feature_x.go"},
		},
		CreatedAt: time.Now().UTC(),
	}

	// Evidence captured on old commit
	evOld := epistemic.NewDeterministicEvidenceRef("EVID-OLD-1", "go-test", oldCommit, "pass", []string{"feature_x.go"})

	req := epistemic.RevisionRequest{
		TargetState: model.ClaimStateVerified,
		Reason:      "Test passed on old commit",
		Actor:       model.AuthorProvenance{AgentID: "codex-1", Harness: "codex-cli"},
		EvidenceRef: &evOld,
	}

	_, _, err := engine.EvaluateAndTransition(ctx, claim, req)
	if err == nil {
		t.Fatalf("expected error: evidence from wrong commit must not verify claim")
	}
}

// Test5_PassingTestMissesChangedLines:
// Passing test misses changed files -> cannot claim coverage-required VERIFIED.
func Test5_PassingTestMissesChangedLines(t *testing.T) {
	ctx := context.Background()
	engine := epistemic.NewEngine()

	commit := "commit-7777"
	claim := model.Claim{
		ID:             "CLAIM-COV-001",
		GoalID:         "GOAL-1",
		GoalRevision:   1,
		Subject:        "security.authz",
		NormalizedText: "Authz role bindings check project ID",
		Scope:          "security.authz",
		Criticality:    model.CriticalityBlocker,
		State:          model.ClaimStateUnsupported,
		Binding: model.CodeBinding{
			CommitSHA: commit,
			Files:     []string{"internal/store/authz_role_bindings.go"},
		},
		CreatedAt: time.Now().UTC(),
	}

	// Test ran and passed, but only covered math.go, not authz_role_bindings.go!
	evPartial := epistemic.NewDeterministicEvidenceRef("EVID-TEST-1", "go-test", commit, "pass", []string{"internal/math/math.go"})

	req := epistemic.RevisionRequest{
		TargetState: model.ClaimStateVerified,
		Reason:      "Some tests passed",
		Actor:       model.AuthorProvenance{AgentID: "codex-1", Harness: "codex-cli"},
		EvidenceRef: &evPartial,
	}

	_, _, err := engine.EvaluateAndTransition(ctx, claim, req)
	if err == nil {
		t.Fatalf("expected error: passing test that misses bound files must be rejected")
	}
}

// Test6_VerificationTheaterDetected:
// Test oracle generated from implementation reproduces the same bug ->
// verification theater detected or downgraded.
func Test6_VerificationTheaterDetected(t *testing.T) {
	ctx := context.Background()
	engine := epistemic.NewEngine()

	commit := "commit-8888"
	claim := model.Claim{
		ID:             "CLAIM-THEATER-001",
		GoalID:         "GOAL-1",
		GoalRevision:   1,
		Subject:        "crypto.hash",
		NormalizedText: "Hash function produces deterministic outputs",
		Scope:          "crypto",
		Criticality:    model.CriticalityBlocker,
		State:          model.ClaimStateUnsupported,
		Binding: model.CodeBinding{
			CommitSHA: commit,
			Files:     []string{"crypto.go"},
		},
		CreatedAt: time.Now().UTC(),
	}

	evTheater := model.EvidenceRef{
		EvidenceID:      "EVID-THEATER-1",
		EvidenceType:    "verification",
		Digest:          "sha256:theater123",
		Tool:            "go-test",
		IsDeterministic: true,
		CommitSHA:       commit,
		CapturedAt:      time.Now().UTC(),
		Summary:         "Test passed against mock oracle",
		CoveredFiles:    []string{"crypto.go"},
		IsOracleDerived: true, // Flagged: oracle derived from implementation under test
	}

	req := epistemic.RevisionRequest{
		TargetState: model.ClaimStateVerified,
		Reason:      "Passes with derived oracle",
		Actor:       model.AuthorProvenance{AgentID: "codex-1", Harness: "codex-cli"},
		EvidenceRef: &evTheater,
	}

	_, _, err := engine.EvaluateAndTransition(ctx, claim, req)
	if err == nil {
		t.Fatalf("expected error: verification theater must be detected and rejected")
	}
}

// Test7_BroadAuthSecureClaimDecomposed:
// Broad "auth secure" claim is decomposed into scoped claims.
func Test7_BroadAuthSecureClaimDecomposed(t *testing.T) {
	ctx := context.Background()
	engine := epistemic.NewEngine()

	broadClaim := model.Claim{
		ID:             "CLAIM-BROAD-001",
		GoalID:         "GOAL-1",
		GoalRevision:   1,
		Subject:        "auth is secure",
		NormalizedText: "All authentication is secure and bug-free",
		Scope:          "auth",
		Criticality:    model.CriticalityBlocker,
		State:          model.ClaimStateUnsupported,
		Author: model.AuthorProvenance{
			AgentID: "claude-code",
			Harness: "claude",
		},
		CreatedAt: time.Now().UTC(),
	}

	subClaims, err := engine.IngestClaim(ctx, broadClaim)
	if err != nil {
		t.Fatalf("IngestClaim: %v", err)
	}

	// Must be decomposed into at least 4 scoped claims
	if len(subClaims) < 4 {
		t.Fatalf("expected at least 4 decomposed claims, got %d", len(subClaims))
	}

	// Verify all sub-claims have concrete sub-scopes and predecessor pointer
	for _, sc := range subClaims {
		if sc.PredecessorID != broadClaim.ID {
			t.Errorf("sub-claim predecessor ID = %s, want %s", sc.PredecessorID, broadClaim.ID)
		}
		if sc.State != model.ClaimStateUnsupported {
			t.Errorf("sub-claim initial state = %s, want %s", sc.State, model.ClaimStateUnsupported)
		}
		if sc.Scope == "auth" || sc.Scope == "" {
			t.Errorf("sub-claim scope %q is not decomposed", sc.Scope)
		}
	}
}

// Test8_RequiredRollbackClaimOmitted_BlocksSuccess:
// Required rollback claim omitted -> Missing Claim blocks SUCCESS.
func Test8_RequiredRollbackClaimOmitted_BlocksSuccess(t *testing.T) {
	engine := epistemic.NewEngine()

	goal := model.GoalContract{
		ID:       "GOAL-ROLLBACK-REQ",
		Revision: 1,
		RequiredCriticalClaims: []string{
			"build.conformance",
			"security.invariants",
			"rollback.atomic_reversion", // Required rollback claim!
		},
	}

	// Only 2 of the 3 required claims exist and are verified
	claims := []model.Claim{
		{
			ID:          "CLAIM-1",
			Subject:     "build.conformance",
			Criticality: model.CriticalityBlocker,
			State:       model.ClaimStateVerified,
		},
		{
			ID:          "CLAIM-2",
			Subject:     "security.invariants",
			Criticality: model.CriticalityBlocker,
			State:       model.ClaimStateVerified,
		},
		// Missing rollback.atomic_reversion!
	}

	report, err := engine.EvaluateGoalEpistemics(goal, claims)
	if err != nil {
		t.Fatalf("EvaluateGoalEpistemics: %v", err)
	}

	if report.CanSucceed {
		t.Fatalf("Goal succeeded despite missing required rollback claim!")
	}
	if len(report.MissingClaims) != 1 || report.MissingClaims[0] != "rollback.atomic_reversion" {
		t.Fatalf("expected missing claim 'rollback.atomic_reversion', got: %v", report.MissingClaims)
	}
}

// Test9_FineGrainedTemporalInvalidation:
// One core file change invalidates only dependent claims, not entire graph.
func Test9_FineGrainedTemporalInvalidation(t *testing.T) {
	engine := epistemic.NewEngine()

	claimAuth := model.Claim{
		ID:          "CLAIM-AUTH",
		Subject:     "auth.token",
		Criticality: model.CriticalityBlocker,
		State:       model.ClaimStateVerified,
		Binding: model.CodeBinding{
			Files: []string{"internal/auth/token.go"},
		},
	}

	claimMath := model.Claim{
		ID:          "CLAIM-MATH",
		Subject:     "math.calc",
		Criticality: model.CriticalityStandard,
		State:       model.ClaimStateVerified,
		Binding: model.CodeBinding{
			Files: []string{"internal/math/calc.go"},
		},
	}

	claims := []model.Claim{claimAuth, claimMath}

	// Modify only internal/auth/token.go
	changedFiles := []string{"internal/auth/token.go"}
	newCommit := "commit-NEW-555"

	updated, transitions := engine.InvalidateOnCodeChange(claims, changedFiles, newCommit)

	if len(updated) != 2 {
		t.Fatalf("expected 2 claims, got %d", len(updated))
	}

	// claimAuth must transition to STALE
	var updatedAuth, updatedMath model.Claim
	for _, c := range updated {
		if c.ID == "CLAIM-AUTH" {
			updatedAuth = c
		} else if c.ID == "CLAIM-MATH" {
			updatedMath = c
		}
	}

	if updatedAuth.State != model.ClaimStateStale {
		t.Fatalf("claimAuth state = %s, want %s", updatedAuth.State, model.ClaimStateStale)
	}

	// Invariant: claimMath MUST NOT become STALE because its bound files were untouched
	if updatedMath.State != model.ClaimStateVerified {
		t.Fatalf("claimMath state = %s, want %s (no whole-graph cascade)",
			updatedMath.State, model.ClaimStateVerified)
	}

	if len(transitions) != 1 || transitions[0].ClaimID != "CLAIM-AUTH" {
		t.Fatalf("expected exactly 1 transition for CLAIM-AUTH, got: %v", transitions)
	}
}

// Test10_UserAssertionCannotVerifyTechnicalSecurityFacts:
// Epistemic invariant: User confirmation can only verify user-observable claims,
// never technical security facts.
func Test10_UserAssertionCannotVerifyTechnicalSecurityFacts(t *testing.T) {
	ctx := context.Background()
	engine := epistemic.NewEngine()

	// Technical security claim
	claim := model.Claim{
		ID:             "CLAIM-SEC-001",
		GoalID:         "GOAL-1",
		GoalRevision:   1,
		Subject:        "security.memory_isolation",
		NormalizedText: "Memory buffers are isolated across tenant boundaries",
		Scope:          "security.memory",
		Criticality:    model.CriticalityBlocker,
		State:          model.ClaimStateUnsupported,
		CreatedAt:      time.Now().UTC(),
	}

	userEv := model.EvidenceRef{
		EvidenceID:   "EVID-USER-1",
		EvidenceType: "user-confirmation",
		Tool:         "user-assertion",
		Summary:      "User says: 'I tested the isolation and it looks secure'",
	}

	req := epistemic.RevisionRequest{
		TargetState: model.ClaimStateVerified,
		Reason:      "User confirmed security",
		Actor:       model.AuthorProvenance{AgentID: "user"},
		EvidenceRef: &userEv,
	}

	_, _, err := engine.EvaluateAndTransition(ctx, claim, req)
	if err == nil {
		t.Fatalf("expected error: user assertion cannot verify technical security facts")
	}
}

// Test11_CircularProvenanceDetected:
// Circular provenance in claim chain is detected and rejected.
func Test11_CircularProvenanceDetected(t *testing.T) {
	dis := epistemic.NewContradictionDiscipline()

	claimsMap := map[string]model.Claim{
		"CLAIM-A": {
			ID:            "CLAIM-A",
			PredecessorID: "CLAIM-B",
		},
		"CLAIM-B": {
			ID:            "CLAIM-B",
			PredecessorID: "CLAIM-C",
		},
		"CLAIM-C": {
			ID:            "CLAIM-C",
			PredecessorID: "CLAIM-A", // Cycle A -> B -> C -> A
		},
	}

	err := dis.DetectCircularProvenance(claimsMap["CLAIM-A"], claimsMap)
	if err == nil {
		t.Fatalf("expected error on circular provenance loop")
	}
}

// Test12_HedgeWordsUncertaintyPresentedAsFact:
// Hedged claims cannot be directly VERIFIED.
func Test12_HedgeWordsUncertaintyPresentedAsFact(t *testing.T) {
	ctx := context.Background()
	engine := epistemic.NewEngine()

	hedgedClaim := model.Claim{
		ID:             "CLAIM-HEDGE-001",
		GoalID:         "GOAL-1",
		GoalRevision:   1,
		Subject:        "perf.latency",
		NormalizedText: "Latency is probably under 5ms in theory",
		Scope:          "perf",
		Criticality:    model.CriticalityStandard,
		State:          model.ClaimStateVerified, // Attempting to present uncertainty as VERIFIED
		Author: model.AuthorProvenance{
			AgentID: "codex-1",
			Harness: "codex-cli",
		},
		CreatedAt: time.Now().UTC(),
	}

	_, err := engine.IngestClaim(ctx, hedgedClaim)
	if err == nil {
		t.Fatalf("expected error on claim presenting uncertainty ('probably') as VERIFIED")
	}
}
