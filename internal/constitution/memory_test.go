package constitution_test

import (
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
)

func validCandidate() constitution.MemoryCandidate {
	return constitution.MemoryCandidate{
		ID:                 "cand-1",
		ProjectID:          "PROJECT-alpha",
		SessionID:          "SESSION-1",
		Class:              constitution.ClassProject,
		Stage:              constitution.StageValidated,
		Fact:               "the store uses sequential schema migrations",
		ProposedBy:         "developer-1",
		EvidenceIDs:        []string{"ev-1"},
		EvidenceFresh:      true,
		IndependentSources: 2,
		ObservedAt:         evalTime,
	}
}

func promotionRequest(candidate constitution.MemoryCandidate) constitution.PromotionRequest {
	return constitution.PromotionRequest{
		Candidate: candidate, ProjectID: "PROJECT-alpha", PromotedBy: "operator-1",
	}
}

func TestValidCandidateIsPromoted(t *testing.T) {
	decision := constitution.GatePromotion(promotionRequest(validCandidate()))
	if !decision.Promote || decision.Stage != constitution.StagePromoted {
		t.Fatalf("a valid candidate was not promoted: %+v", decision)
	}
}

// The central rule: a model may propose but never write canonical memory.
func TestAICannotBeThePromotingAuthority(t *testing.T) {
	request := promotionRequest(validCandidate())
	request.PromotedByIsAI = true
	request.PromotedBy = "control-intelligence"
	decision := constitution.GatePromotion(request)
	if decision.Promote {
		t.Fatal("a model promoted canonical memory directly")
	}
	if decision.Reason != constitution.ReasonMemoryPromotionRefused {
		t.Fatalf("refused for %s, want memory promotion refused", decision.Reason)
	}

	// An absent promoting principal is equally refused.
	request.PromotedByIsAI = false
	request.PromotedBy = ""
	if constitution.GatePromotion(request).Promote {
		t.Fatal("memory was promoted with no authorizing principal")
	}
}

// A model-proposed fact needs evidence. Confidence and repetition are not
// substitutes: repeating an unevidenced claim never promotes it.
func TestAIProposedFactRequiresFreshEvidence(t *testing.T) {
	unevidenced := validCandidate()
	unevidenced.ProposedByAI = true
	unevidenced.EvidenceIDs = nil
	for i := 0; i < 5; i++ {
		if constitution.GatePromotion(promotionRequest(unevidenced)).Promote {
			t.Fatal("an unevidenced model claim was promoted")
		}
	}

	stale := validCandidate()
	stale.ProposedByAI = true
	stale.EvidenceFresh = false
	if constitution.GatePromotion(promotionRequest(stale)).Promote {
		t.Fatal("a model claim on stale evidence was promoted")
	}

	evidenced := validCandidate()
	evidenced.ProposedByAI = true
	if !constitution.GatePromotion(promotionRequest(evidenced)).Promote {
		t.Fatal("a properly evidenced model claim was refused")
	}
}

// Attack: skip validation and present a candidate straight for promotion.
func TestUnvalidatedCandidateCannotBePromoted(t *testing.T) {
	for _, stage := range []constitution.CandidateStage{
		constitution.StageProposed, constitution.StageRejected, constitution.StagePromoted, "FORGED",
	} {
		candidate := validCandidate()
		candidate.Stage = stage
		if constitution.GatePromotion(promotionRequest(candidate)).Promote {
			t.Fatalf("a candidate at stage %s bypassed validation", stage)
		}
	}
}

// Attack: poison another project's memory.
func TestCrossProjectCandidateIsRejected(t *testing.T) {
	candidate := validCandidate()
	candidate.ProjectID = "PROJECT-beta"
	decision := constitution.ValidateCandidate(candidate, "PROJECT-alpha")
	if decision.Stage != constitution.StageRejected || decision.Reason != constitution.ReasonCrossProjectLeak {
		t.Fatalf("a cross-project candidate was not rejected: %+v", decision)
	}
	if constitution.GatePromotion(promotionRequest(candidate)).Promote {
		t.Fatal("a cross-project candidate was promoted")
	}
}

// Secrets and hidden reasoning never become canonical memory.
func TestSecretsAndHiddenReasoningAreRejected(t *testing.T) {
	secret := validCandidate()
	secret.ContainsSecret = true
	if constitution.ValidateCandidate(secret, "PROJECT-alpha").Stage != constitution.StageRejected {
		t.Fatal("a candidate carrying credential material was accepted")
	}
	if constitution.GatePromotion(promotionRequest(secret)).Promote {
		t.Fatal("a candidate carrying credential material was promoted")
	}

	reasoning := validCandidate()
	reasoning.ContainsHiddenReasoning = true
	if constitution.ValidateCandidate(reasoning, "PROJECT-alpha").Stage != constitution.StageRejected {
		t.Fatal("a candidate carrying hidden model reasoning was accepted")
	}
}

// Orchestration memory must not become a side channel for project content.
func TestControlIntelligenceMemoryCannotCarryProjectContent(t *testing.T) {
	candidate := validCandidate()
	candidate.Class = constitution.ClassControlIntelligence
	candidate.EvidenceIDs = []string{"project:internal/secret.go"}
	if constitution.ValidateCandidate(candidate, "PROJECT-alpha").Stage != constitution.StageRejected {
		t.Fatal("orchestration memory was allowed to reference project content")
	}
}

// Three agents repeating one output are one source, not three. The threshold
// counts distinct sources so that self-reinforcement cannot clear it.
func TestIndependentSourceThreshold(t *testing.T) {
	candidate := validCandidate()
	candidate.IndependentSources = 1
	request := promotionRequest(candidate)
	request.RequiredIndependentSources = 3
	if constitution.GatePromotion(request).Promote {
		t.Fatal("a single source satisfied a three-source requirement")
	}
	candidate.IndependentSources = 3
	request.Candidate = candidate
	if !constitution.GatePromotion(request).Promote {
		t.Fatal("three independent sources did not satisfy the requirement")
	}
}

// An AI review can raise concerns and can never clear them.
func TestReviewConcernsBlockPromotion(t *testing.T) {
	request := promotionRequest(validCandidate())
	request.ReviewedByAI = true
	request.ReviewConcerns = []string{"this contradicts what I saw earlier"}
	if constitution.GatePromotion(request).Promote {
		t.Fatal("promotion proceeded despite unresolved review concerns")
	}

	// A review that raises nothing does not itself authorize anything: the
	// candidate is promoted on its evidence, exactly as it would be without a
	// review at all.
	request.ReviewConcerns = nil
	withReview := constitution.GatePromotion(request)
	request.ReviewedByAI = false
	withoutReview := constitution.GatePromotion(request)
	if withReview.Promote != withoutReview.Promote {
		t.Fatal("an AI review changed the promotion outcome on its own")
	}
}

// Contradictions are held for reconciliation rather than silently overwriting
// the existing truth.
func TestContradictionRequiresReconciliation(t *testing.T) {
	request := promotionRequest(validCandidate())
	request.ExistingContradictions = []string{"mem-9"}
	decision := constitution.GatePromotion(request)
	if decision.Promote {
		t.Fatal("a contradicting fact silently overwrote canonical memory")
	}
	if !decision.RequiresReconciliation {
		t.Fatal("the contradiction was not flagged for reconciliation")
	}
}

// Invalidating a fact must stale everything that rested on it, transitively.
func TestStalenessPropagatesTransitively(t *testing.T) {
	dependents := map[string][]string{
		"arch-fact":           {"security-assumption", "recommendation-1"},
		"security-assumption": {"claim-7"},
		"claim-7":             {"report-2"},
	}
	result := constitution.PropagateStaleness("arch-fact", dependents)
	expected := map[string]bool{
		"security-assumption": true, "recommendation-1": true, "claim-7": true, "report-2": true,
	}
	if len(result.Stale) != len(expected) {
		t.Fatalf("staleness reached %v, want %d entries", result.Stale, len(expected))
	}
	for _, id := range result.Stale {
		if !expected[id] {
			t.Fatalf("unexpected entry %q in the staleness set", id)
		}
	}
}

// A cycle in recorded dependencies must terminate rather than hang.
func TestStalenessPropagationTerminatesOnCycles(t *testing.T) {
	cyclic := map[string][]string{"a": {"b"}, "b": {"c"}, "c": {"a", "b"}}
	done := make(chan constitution.StalenessPropagation, 1)
	go func() { done <- constitution.PropagateStaleness("a", cyclic) }()
	select {
	case result := <-done:
		if len(result.Stale) != 2 {
			t.Fatalf("cyclic propagation returned %v, want b and c", result.Stale)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("staleness propagation did not terminate on a cyclic dependency graph")
	}
}

// Violations map to fixed responses; the same breach always draws the same one.
func TestViolationResponsesAreFixedAndFailSevere(t *testing.T) {
	if constitution.ResponseFor(constitution.ViolationMemoryPoisoning) != constitution.ResponseSuspend {
		t.Fatal("memory poisoning did not suspend the session")
	}
	if constitution.ResponseFor(constitution.ViolationSandboxBypass) != constitution.ResponseRevoke {
		t.Fatal("a sandbox bypass did not revoke the grant that permitted it")
	}
	// An unrecognised class is treated as the most serious case.
	if constitution.ResponseFor(constitution.ViolationClass("INVENTED")) != constitution.ResponseSuspend {
		t.Fatal("an unrecognised violation class was not treated as severe")
	}
	if !constitution.ResponseSuspend.Halting() || constitution.ResponseBlock.Halting() {
		t.Fatal("halting classification is wrong")
	}
}

// A verdict that merely requires approval is the constitution working, not a
// security breach, and must not be recorded as one.
func TestLegitimateRefusalsAreNotViolations(t *testing.T) {
	env := cleanEnvelope()
	state := cleanState()
	state.HarnessGovernance = constitution.GovernanceDegraded
	verdict := evaluate(t, env, state, nil)
	if violations := constitution.ViolationsFrom(verdict, env, evalTime); len(violations) != 0 {
		t.Fatalf("an honest degrade was recorded as %d violation(s): %+v", len(violations), violations)
	}
}

// A genuine bypass attempt is recorded with the mandated response.
func TestBypassAttemptsBecomeViolations(t *testing.T) {
	env := cleanEnvelope()
	env.Domain = constitution.DomainShell
	state := cleanState()
	state.SandboxAvailable = false
	state.ForeignProjectRefs = []string{"PROJECT-beta"}
	verdict := evaluate(t, env, state, nil)

	violations := constitution.ViolationsFrom(verdict, env, evalTime)
	if len(violations) < 2 {
		t.Fatalf("expected sandbox and cross-project violations, got %+v", violations)
	}
	seen := map[constitution.ViolationClass]bool{}
	for _, violation := range violations {
		seen[violation.Class] = true
		if violation.ProjectID != env.ProjectID || violation.SessionID != env.SessionID {
			t.Fatal("a violation was not bound to its project and session")
		}
	}
	if !seen[constitution.ViolationSandboxBypass] || !seen[constitution.ViolationCrossProjectLeak] {
		t.Fatalf("expected classes were not recorded: %+v", seen)
	}

	// The session is held to the strongest response it earned.
	response, ok := constitution.MostSevereResponse(violations)
	if !ok || response != constitution.ResponseSuspend {
		t.Fatalf("most severe response was %s, want SUSPEND", response)
	}
}
