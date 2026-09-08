package constitution_test

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/constitution"
)

func restored(kind, ref string) constitution.RestoreTarget {
	return constitution.RestoreTarget{
		Kind: kind, Ref: ref,
		ExpectedDigest: "sha256:before", ObservedDigest: "sha256:before", Observed: true,
	}
}

func TestVerifiedRollbackRequiresEveryTargetRestored(t *testing.T) {
	assessment := constitution.AssessRollback(constitution.RollbackRequest{
		Envelope: cleanEnvelope(),
		Targets: []constitution.RestoreTarget{
			restored("worktree", "main"), restored("goal", "GOAL-1"), restored("claim", "c-1"),
		},
		Now: evalTime,
	})
	if assessment.Status != constitution.RollbackVerified {
		t.Fatalf("a fully restored rollback produced %s: %+v", assessment.Status, assessment)
	}
	if !assessment.Status.Restored() {
		t.Fatal("the verified status did not report as restored")
	}
}

// The central failure this guards against: an audit record saying a rollback
// happened, with no target actually restored.
func TestAuditOnlyRollbackIsNotSuccess(t *testing.T) {
	assessment := constitution.AssessRollback(constitution.RollbackRequest{
		Envelope: cleanEnvelope(), Targets: nil, Now: evalTime,
	})
	if assessment.Status.Restored() {
		t.Fatal("a rollback with nothing checked was reported as restored")
	}
	if assessment.Status != constitution.RollbackUnverifiable {
		t.Fatalf("status was %s, want ROLLBACK_UNVERIFIED", assessment.Status)
	}
	if assessment.Reason != constitution.ReasonRollbackUnverified {
		t.Fatalf("reason was %s, want rollback unverified", assessment.Reason)
	}
}

// A target whose observed state differs from the intended one is not restored,
// however confidently the rollback was recorded.
func TestUnrestoredTargetPreventsSuccess(t *testing.T) {
	stillChanged := constitution.RestoreTarget{
		Kind: "worktree", Ref: "main",
		ExpectedDigest: "sha256:before", ObservedDigest: "sha256:after", Observed: true,
	}
	assessment := constitution.AssessRollback(constitution.RollbackRequest{
		Envelope: cleanEnvelope(),
		Targets:  []constitution.RestoreTarget{stillChanged},
		Now:      evalTime,
	})
	if assessment.Status.Restored() {
		t.Fatal("a worktree still holding the changed state was reported as rolled back")
	}
	if assessment.Status != constitution.RollbackFailed {
		t.Fatalf("status was %s, want ROLLBACK_FAILED", assessment.Status)
	}
	if len(assessment.UnrestoredTargets) != 1 {
		t.Fatalf("the unrestored target was not named: %+v", assessment.UnrestoredTargets)
	}
}

// A mixed outcome is reported as partial, not rounded up to success.
func TestPartialRollbackIsReportedAsPartial(t *testing.T) {
	assessment := constitution.AssessRollback(constitution.RollbackRequest{
		Envelope: cleanEnvelope(),
		Targets: []constitution.RestoreTarget{
			restored("worktree", "main"),
			{Kind: "goal", Ref: "GOAL-1", ExpectedDigest: "sha256:before",
				ObservedDigest: "sha256:after", Observed: true},
		},
		Now: evalTime,
	})
	if assessment.Status != constitution.RollbackPartial {
		t.Fatalf("a mixed rollback produced %s, want PARTIALLY_ROLLED_BACK", assessment.Status)
	}
	if assessment.Status.Restored() {
		t.Fatal("a partial rollback was reported as restored")
	}
}

// A target that could not be read back is not assumed restored: silence is
// never treated as success.
func TestUnobservableTargetIsNotAssumedRestored(t *testing.T) {
	assessment := constitution.AssessRollback(constitution.RollbackRequest{
		Envelope: cleanEnvelope(),
		Targets: []constitution.RestoreTarget{
			restored("worktree", "main"),
			{Kind: "claim", Ref: "c-9", ExpectedDigest: "sha256:before", Observed: false},
		},
		Now: evalTime,
	})
	if assessment.Status.Restored() {
		t.Fatal("an unreadable target was assumed restored")
	}
	if assessment.Status != constitution.RollbackUnverifiable {
		t.Fatalf("status was %s, want ROLLBACK_UNVERIFIED", assessment.Status)
	}
	if len(assessment.UnobservableTargets) != 1 {
		t.Fatalf("the unobservable target was not named: %+v", assessment.UnobservableTargets)
	}
}

// A target with no expected digest proves nothing: there is nothing to compare
// the observation against.
func TestTargetWithNoExpectedStateIsNotRestored(t *testing.T) {
	target := constitution.RestoreTarget{
		Kind: "worktree", Ref: "main", ObservedDigest: "sha256:whatever", Observed: true,
	}
	if target.Restored() {
		t.Fatal("a target with no intended state reported itself as restored")
	}
	assessment := constitution.AssessRollback(constitution.RollbackRequest{
		Envelope: cleanEnvelope(), Targets: []constitution.RestoreTarget{target}, Now: evalTime,
	})
	if assessment.Status.Restored() {
		t.Fatal("a rollback with no intended state was reported as restored")
	}
}

// External effects are disclosed even when the local restore is complete,
// because a user reading "rolled back" would otherwise believe them reversed.
func TestExternalEffectsAreDisclosedOnSuccess(t *testing.T) {
	assessment := constitution.AssessRollback(constitution.RollbackRequest{
		Envelope:        cleanEnvelope(),
		Targets:         []constitution.RestoreTarget{restored("worktree", "main")},
		ExternalEffects: []string{"deployed release v2 to production"},
		Now:             evalTime,
	})
	if assessment.Status != constitution.RollbackVerified {
		t.Fatalf("a complete local restore produced %s", assessment.Status)
	}
	if len(assessment.ExternalEffects) != 1 {
		t.Fatal("the external effect was not disclosed")
	}
	if !strings.Contains(assessment.Explanation, "outside this project") {
		t.Fatalf("the explanation does not disclose the external effect: %q", assessment.Explanation)
	}
}

// Evidence and memory that rested on the undone state are surfaced for
// invalidation, so a rollback does not leave stale proof standing.
func TestDependentEvidenceAndMemoryAreSurfaced(t *testing.T) {
	assessment := constitution.AssessRollback(constitution.RollbackRequest{
		Envelope:          cleanEnvelope(),
		Targets:           []constitution.RestoreTarget{restored("worktree", "main")},
		DependentEvidence: []string{"ev-2", "ev-1"},
		DependentMemory:   []string{"mem-3"},
		Now:               evalTime,
	})
	if len(assessment.StaleEvidence) != 2 || assessment.StaleEvidence[0] != "ev-1" {
		t.Fatalf("dependent evidence was not surfaced in stable order: %+v", assessment.StaleEvidence)
	}
	if len(assessment.StaleMemory) != 1 {
		t.Fatalf("dependent memory was not surfaced: %+v", assessment.StaleMemory)
	}
}

// The gate and the assessment must agree: a rollback the assessment refuses to
// call restored is one the gate refuses to allow.
func TestGateAndRollbackAssessmentAgree(t *testing.T) {
	unrestored := constitution.RollbackRequest{
		Envelope: cleanEnvelope(),
		Targets: []constitution.RestoreTarget{{
			Kind: "worktree", Ref: "main", ExpectedDigest: "sha256:before",
			ObservedDigest: "sha256:after", Observed: true,
		}},
		Now: evalTime,
	}
	assessment := constitution.AssessRollback(unrestored)

	env := cleanEnvelope()
	env.Domain = constitution.DomainRollback
	state := cleanState()
	state.RollbackVerified = assessment.Status.Restored()

	verdict := evaluate(t, env, state, nil)
	if verdict.Outcome == constitution.OutcomeAllow {
		t.Fatal("the gate allowed a rollback the assessment refused to call restored")
	}
}
