package verification

import (
	"testing"
	"time"
)

func TestPlannerDiscoversMissingCriticalClaims(t *testing.T) {
	criteria, missing, err := Plan(PlanRequest{Criteria: []string{"security", "behavior"}, CriticalCriteria: map[string]bool{"security": true}, Claims: []Claim{{ID: "c", CriterionID: "behavior", SemanticScope: []string{"runtime"}}}, RiskTier: "high"})
	if err != nil || len(criteria) != 2 || len(missing) != 1 || missing[0] != "security" {
		t.Fatalf("%v %v %v", criteria, missing, err)
	}
}
func TestReviewTheReviewerAndIndependenceRequired(t *testing.T) {
	reviews := []Review{{ID: "a", Reviewer: "alice", Provider: "p1", Implementation: "i1", SubjectDigest: "tree", EvidenceID: "e1", Result: StatusPass, ReviewsReviewID: "meta-a"}, {ID: "b", Reviewer: "bob", Provider: "p2", Implementation: "i2", SubjectDigest: "tree", EvidenceID: "e2", Result: StatusPass, ReviewsReviewID: "meta-b"}}
	if ReviewQuorum("tree", reviews, 2) != StatusPass {
		t.Fatal("independent reviewed quorum rejected")
	}
	reviews[1].Implementation = "i1"
	if ReviewQuorum("tree", reviews, 2) != StatusBlocked {
		t.Fatal("shared reviewer implementation accepted")
	}
}
func TestRegressionOracleInvalidatesOnTreeChange(t *testing.T) {
	s := fixture(time.Now())
	ids := InvalidateEvidence(&s, "changed", "mutation", time.Now())
	if len(ids) != 2 || s.Evidence[0].Status != StatusUnknown || len(s.KnownBlockers) == 0 {
		t.Fatalf("%v %+v", ids, s)
	}
}
func TestVerificationBudgetFailsClosed(t *testing.T) {
	now := time.Now()
	b := VerificationBudget{MaxChecks: 1, MaxProviderCalls: 1, Deadline: now.Add(time.Hour)}
	if err := b.Consume(true, now); err != nil {
		t.Fatal(err)
	}
	if err := b.Consume(false, now); err == nil {
		t.Fatal("budget overrun accepted")
	}
}
func TestMutationMustBeIsolatedFromCanonicalState(t *testing.T) {
	m := MutationResult{ID: "m", Target: "oracle", Operator: "invert", Isolated: true, CanonicalBefore: "tree", CanonicalAfter: "tree", OracleStatus: StatusPass}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	m.CanonicalAfter = "mutated"
	if err := m.Validate(); err == nil {
		t.Fatal("canonical mutation accepted")
	}
}
