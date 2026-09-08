package constitution_test

import (
	"math"
	"testing"

	"github.com/Zen1th53/marshal/internal/constitution"
)

func passing(id string) constitution.Criterion {
	return constitution.Criterion{
		ID: id, Text: id, Status: constitution.CriterionPass,
		EvidenceIDs: []string{"ev-" + id}, EvidenceFresh: true,
	}
}

func completionRequest(criteria ...constitution.Criterion) constitution.CompletionRequest {
	env := cleanEnvelope()
	env.Domain = constitution.DomainCompletion
	return constitution.CompletionRequest{
		Envelope: env, Criteria: criteria, FinalStateDigest: "sha256:final",
	}
}

func TestVerifiedRequiresEveryCriterionPassing(t *testing.T) {
	result := constitution.AssessCompletion(completionRequest(passing("a"), passing("b")))
	if !result.Verified() {
		t.Fatalf("all criteria passing did not yield VERIFIED: %s %v", result.Status, result.Blockers)
	}
	if math.Abs(result.Alignment.CoveragePercent-100) > 1e-9 {
		t.Fatalf("coverage was %.4f, want 100", result.Alignment.CoveragePercent)
	}
}

// With no criteria there is nothing to measure. The honest answer is
// INCONCLUSIVE with no score, never a comforting number.
func TestNoCriteriaIsInconclusiveWithNoScore(t *testing.T) {
	result := constitution.AssessCompletion(completionRequest())
	if result.Status != constitution.CompletionInconclusive {
		t.Fatalf("no criteria produced %s, want INCONCLUSIVE", result.Status)
	}
	if result.Alignment.Derivable {
		t.Fatal("a coverage score was reported with no criteria to derive it from")
	}
	if result.Verified() {
		t.Fatal("work with no acceptance criteria was marked verified")
	}
}

// A PASS asserted without evidence, or on stale evidence, is not admissible
// and degrades to UNKNOWN rather than counting toward completion.
func TestUnevidencedOrStalePassBecomesUnknown(t *testing.T) {
	noEvidence := constitution.Criterion{ID: "a", Status: constitution.CriterionPass, EvidenceFresh: true}
	stale := constitution.Criterion{
		ID: "b", Status: constitution.CriterionPass,
		EvidenceIDs: []string{"ev-b"}, EvidenceFresh: false,
	}
	for name, criterion := range map[string]constitution.Criterion{
		"no evidence": noEvidence, "stale evidence": stale,
	} {
		t.Run(name, func(t *testing.T) {
			result := constitution.AssessCompletion(completionRequest(criterion))
			if result.Verified() {
				t.Fatalf("a PASS with %s was accepted as verified", name)
			}
			if result.Alignment.Unknown != 1 {
				t.Fatalf("a PASS with %s was not downgraded to UNKNOWN: %+v", name, result.Alignment)
			}
			if result.Alignment.CoveragePercent != 0 {
				t.Fatalf("a PASS with %s still contributed %.2f%% coverage", name, result.Alignment.CoveragePercent)
			}
		})
	}
}

// An unmet critical criterion caps the result whatever the others say.
func TestUnmetCriticalCriterionBlocksVerification(t *testing.T) {
	critical := constitution.Criterion{
		ID: "critical", Status: constitution.CriterionFail, Critical: true,
		EvidenceIDs: []string{"ev"}, EvidenceFresh: true,
	}
	result := constitution.AssessCompletion(completionRequest(passing("a"), passing("b"), passing("c"), critical))
	if result.Verified() {
		t.Fatal("an unmet critical criterion was verified anyway")
	}
	if result.Alignment.CriticalUnmet != 1 {
		t.Fatalf("the unmet critical criterion was not counted: %+v", result.Alignment)
	}
	if result.Status != constitution.CompletionPartial {
		t.Fatalf("status was %s, want PARTIAL", result.Status)
	}
}

// The score is reproducible and hand-checkable from the criteria alone.
func TestAlignmentIsReproducibleAndWeighted(t *testing.T) {
	criteria := []constitution.Criterion{
		{ID: "a", Status: constitution.CriterionPass, Weight: 3, EvidenceIDs: []string{"e"}, EvidenceFresh: true},
		{ID: "b", Status: constitution.CriterionPartial, Weight: 1, EvidenceIDs: []string{"e"}, EvidenceFresh: true},
		{ID: "c", Status: constitution.CriterionFail, Weight: 1, EvidenceIDs: []string{"e"}, EvidenceFresh: true},
	}
	// satisfied = 3 + 0.5 = 3.5 of 5 total weight = 70%.
	first := constitution.ScoreAlignment(criteria)
	if math.Abs(first.CoveragePercent-70) > 1e-9 {
		t.Fatalf("coverage was %.4f, want 70", first.CoveragePercent)
	}
	for i := 0; i < 10; i++ {
		if constitution.ScoreAlignment(criteria).CoveragePercent != first.CoveragePercent {
			t.Fatal("scoring the same criteria twice gave different answers")
		}
	}
	// A zero weight must not erase a criterion from the denominator.
	zeroWeighted := constitution.ScoreAlignment([]constitution.Criterion{
		{ID: "a", Status: constitution.CriterionPass, Weight: 0, EvidenceIDs: []string{"e"}, EvidenceFresh: true},
		{ID: "b", Status: constitution.CriterionFail, Weight: 0, EvidenceIDs: []string{"e"}, EvidenceFresh: true},
	})
	if math.Abs(zeroWeighted.CoveragePercent-50) > 1e-9 {
		t.Fatalf("zero-weight criteria produced %.4f%%, want 50", zeroWeighted.CoveragePercent)
	}
}

// An unrecognised status is never treated as success.
func TestUnrecognisedStatusIsNotSuccess(t *testing.T) {
	alignment := constitution.ScoreAlignment([]constitution.Criterion{
		{ID: "a", Status: constitution.CriterionStatus("DEFINITELY_FINE"), EvidenceIDs: []string{"e"}, EvidenceFresh: true},
	})
	if alignment.Passed != 0 || alignment.Unknown != 1 || alignment.CoveragePercent != 0 {
		t.Fatalf("an invented criterion status counted toward completion: %+v", alignment)
	}
}

// Separation of duties: work is not reviewed by the person who did it.
func TestSelfReviewDoesNotSatisfyIndependentReview(t *testing.T) {
	request := completionRequest(passing("a"))
	request.IndependentReviewRequired = true
	request.IndependentReviewDone = true
	request.ReviewerID = request.Envelope.Actor
	result := constitution.AssessCompletion(request)
	if result.Verified() {
		t.Fatal("a self-review satisfied the independent review requirement")
	}

	request.ReviewerID = "reviewer-2"
	if !constitution.AssessCompletion(request).Verified() {
		t.Fatal("a genuine independent review did not satisfy the requirement")
	}

	request.IndependentReviewDone = false
	if constitution.AssessCompletion(request).Verified() {
		t.Fatal("a missing independent review was verified anyway")
	}
}

func TestOutstandingBlockersPreventVerification(t *testing.T) {
	cases := map[string]struct {
		mutate func(*constitution.CompletionRequest)
		want   constitution.CompletionStatus
	}{
		"unresolved conflict": {
			func(r *constitution.CompletionRequest) { r.BlockingConflicts = []string{"claims disagree"} },
			constitution.CompletionBlocked,
		},
		"outstanding approval": {
			func(r *constitution.CompletionRequest) { r.OutstandingApprovals = []string{"deploy"} },
			constitution.CompletionNeedsUserDecision,
		},
		"unverified rollback": {
			func(r *constitution.CompletionRequest) { r.RollbackRequired = true },
			constitution.CompletionBlocked,
		},
		"no final state digest": {
			func(r *constitution.CompletionRequest) { r.FinalStateDigest = "" },
			constitution.CompletionPartial,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			request := completionRequest(passing("a"))
			tc.mutate(&request)
			result := constitution.AssessCompletion(request)
			if result.Verified() {
				t.Fatalf("%s was verified anyway", name)
			}
			if result.Status != tc.want {
				t.Fatalf("%s produced %s, want %s", name, result.Status, tc.want)
			}
			if len(result.Blockers) == 0 {
				t.Fatalf("%s produced no user-visible blocker", name)
			}
		})
	}
}

func TestFailedVersusPartialDistinction(t *testing.T) {
	failing := constitution.Criterion{
		ID: "a", Status: constitution.CriterionFail, EvidenceIDs: []string{"e"}, EvidenceFresh: true,
	}
	if status := constitution.AssessCompletion(completionRequest(failing)).Status; status != constitution.CompletionFailed {
		t.Fatalf("everything failing produced %s, want FAILED", status)
	}
	mixed := constitution.AssessCompletion(completionRequest(passing("ok"), failing))
	if mixed.Status != constitution.CompletionPartial {
		t.Fatalf("mixed results produced %s, want PARTIAL", mixed.Status)
	}
	if len(mixed.Alignment.UnmetIDs) != 1 || mixed.Alignment.UnmetIDs[0] != "a" {
		t.Fatalf("the unmet criterion was not named: %+v", mixed.Alignment.UnmetIDs)
	}
}
