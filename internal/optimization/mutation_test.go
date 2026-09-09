package optimization

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/learning"
)

// These are mutation killers for the hard gates named by Process 08. Each
// assertion is intentionally behavioral: deleting or bypassing the relevant
// gate turns a previously refused unsafe state into an accepted one.
func TestMutationKillersForCriticalGates(t *testing.T) {
	base := Baseline{ID: "base", MarshalSHA: "sha", RoutingConfig: "route", VerifierPolicy: "verify", Toolchain: "tool", EnvironmentHash: "env"}
	candidate := adversarialCandidate()
	result := ExperimentResult{ID: "r", TaskID: "t", TaskClass: "code", Outcome: StatusPass, ClusterID: "cluster"}
	if record, _ := Promote(PromotionInput{Candidate: candidate, Baseline: base, CandidateBaseline: base, Results: []ExperimentResult{result}, Reproducible: true}, Governance{MinEvidenceClusters: 2}, time.Now()); record.Decision != DecisionNeedsEvidence {
		t.Fatalf("single-source evidence promoted: %s", record.Decision)
	}
	changed := base
	changed.EnvironmentHash = "changed"
	if record, _ := Promote(PromotionInput{Candidate: candidate, Baseline: base, CandidateBaseline: changed, Results: []ExperimentResult{result}, Reproducible: true}, Governance{MinEvidenceClusters: 1}, time.Now()); record.Decision != DecisionNeedsEvidence {
		t.Fatalf("uncomparable baseline promoted: %s", record.Decision)
	}
	if record, _ := Promote(PromotionInput{Candidate: candidate, Baseline: base, CandidateBaseline: base, Results: []ExperimentResult{{ID: "u", TaskID: "u", Outcome: StatusUnknown, ClusterID: "cluster"}}, Reproducible: true}, Governance{MinEvidenceClusters: 1}, time.Now()); record.Decision != DecisionNeedsEvidence {
		t.Fatalf("UNKNOWN promoted: %s", record.Decision)
	}
	if err := ValidateShadow(ShadowPolicy{MaxCostMicros: 1, EligibleTaskClasses: []string{"code"}, AllowExternalEffects: true}, "code"); !errors.Is(err, ErrVeto) {
		t.Fatalf("shadow side effect allowed: %v", err)
	}
	if err := ValidateCanary(Canary{ID: "c", CandidateID: "x", BaselineID: "b", Exposure: 1, EligibleTaskClasses: []string{"code"}, MaxTasks: 1, Deadline: time.Now().Add(time.Hour), RollbackTriggers: []Trigger{{Metric: "errors", Threshold: 1, HigherIsWorse: true}}}, Governance{MaxCanaryExposure: 1}); !errors.Is(err, ErrVeto) {
		t.Fatalf("global canary allowed: %v", err)
	}
}

func TestMutationKillerReplaySafetyAndIntegrity(t *testing.T) {
	factual := cfFactual(learning.OutcomeFailed, StatusFail)
	factual.SideEffects = []SideEffect{{Kind: "delete", Destructive: true}}
	if err := SafeToEvaluate(factual, MethodReplay); !errors.Is(err, ErrUnsafeCounterfactual) {
		t.Fatalf("unsafe replay accepted: %v", err)
	}
	runner := &recordingReplayRunner{}
	sandbox := cfSandbox()
	sandbox.ProductionCredentials = true
	_, err := ExecuteReplay(context.Background(), runner, cfFactual(learning.OutcomeFailed, StatusFail), cfRoute("claude"), sandbox, "test", "cluster", cfGovernance(), cfNow())
	if !errors.Is(err, ErrUnsafeCounterfactual) || runner.called {
		t.Fatalf("credentialed replay ran: err=%v called=%t", err, runner.called)
	}
	result, err := NewExperimentResult(ExperimentResult{ID: "r", TaskID: "t", Outcome: StatusPass}, cfNow())
	if err != nil {
		t.Fatal(err)
	}
	result.ClusterID = "forged"
	if err := result.Verify(); !errors.Is(err, ErrTampered) {
		t.Fatalf("result tamper hidden: %v", err)
	}
}
