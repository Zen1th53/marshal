package verification

import (
	"testing"
	"time"
)

func actionSession() Session {
	now := time.Now().UTC()
	return Session{ID: "verify-action", Version: 1, Binding: Binding{ProjectID: "project", GoalID: "goal", GoalRevision: 1, PlanID: "plan", PlanVersion: 1, RunID: "run", RunVersion: 1, TreeDigest: "tree", EnvironmentDigest: "environment"}, State: VerifiedComplete, Criteria: []Criterion{{ID: "criterion", Mandatory: true, ClaimIDs: []string{"claim"}}}, Claims: []Claim{{ID: "claim", CriterionID: "criterion", SemanticScope: []string{"tests"}, EvidenceIDs: []string{"evidence"}}}, Evidence: []Evidence{{ID: "evidence", ClaimID: "claim", Status: StatusPass, ContentDigest: "digest", TreeDigest: "tree", EnvironmentDigest: "environment", Attempts: 1, Passes: 1}}, RequiredChecks: map[string]Status{"checks": StatusPass}, CreatedAt: now, UpdatedAt: now}
}
func TestVerificationChallengeInvalidatesAttestation(t *testing.T) {
	session := actionSession()
	a, err := NewCompletionAttestation("old", session, "bundle", "operator", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	next, err := Challenge(session, ChallengeInput{ClaimID: "claim", EvidenceID: "counter", EvidenceDigest: "counter-digest", Reason: "counter example"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if next.Version != a.VerificationVersion+1 || Evaluate(next, next.Binding, time.Now()) == VerifiedComplete {
		t.Fatal("challenge preserved completion")
	}
	if AttestationCurrent(a, next) == nil {
		t.Fatal("old attestation accepted after challenge")
	}
}
func TestFindingClosureCannotWaiveCriticalEvidence(t *testing.T) {
	session := actionSession()
	next, err := Challenge(session, ChallengeInput{ClaimID: "claim", EvidenceID: "counter", EvidenceDigest: "counter-digest", Reason: "counter example"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, err = CloseFinding(next, FindingClosure{FindingID: next.Contradictions[0].ID, Actor: "operator", Reason: "ignore it", ExpiresAt: time.Now().Add(time.Hour)}, time.Now())
	if err != ErrUnsafeWaiver {
		t.Fatalf("mandatory finding waiver: %v", err)
	}
}
