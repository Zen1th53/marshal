package optimization

import (
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/learning"
)

func adversarialCandidate() Candidate {
	return Candidate{ID: "candidate", Dimension: DimRouting, Hypothesis: "use measured alternate route", TaskScope: []string{"code"}, RollbackPlan: "restore baseline", VerificationPlan: "independent verifier", Provenance: "test"}
}

// Every hard-governance mutation in the Process 08 acceptance pack must be
// stopped before any score, benchmark, or provider claim is considered.
func TestAdversarialHardGovernanceVetoes(t *testing.T) {
	cases := []struct {
		name  string
		apply func(*Effects)
	}{
		{"approval bypass", func(e *Effects) { e.WeakensApprovals = true }},
		{"cheap candidate skips verification", func(e *Effects) { e.RemovesMandatoryVerification = true }},
		{"hidden unknown", func(e *Effects) { e.HidesUnknown = true }},
		{"network weakening", func(e *Effects) { e.WeakensNetwork = true }},
		{"shadow secret exposure", func(e *Effects) { e.ExposesSecrets = true }},
		{"self modifying governance", func(e *Effects) { e.ModifiesGovernance = true }},
		{"verification reserve theft", func(e *Effects) { e.SpendsVerificationReserve = true }},
		{"community fleet control", func(e *Effects) { e.EnablesFleetControl = true }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := adversarialCandidate()
			tc.apply(&c.Effects)
			if reasons := Veto(c, Governance{RequireRollback: true}); len(reasons) == 0 {
				t.Fatal("unsafe candidate was not vetoed")
			}
			if !RequiresLifecycleReturn(c) {
				t.Fatal("material candidate avoided lifecycle return")
			}
		})
	}
}

func TestAdversarialCounterfactualSafetyAndUnknownHandling(t *testing.T) {
	f := cfFactual(learning.OutcomeFailed, StatusFail)
	f.SideEffects = []SideEffect{{Kind: "payment", Destructive: true, Target: "billing"}}
	if err := SafeToEvaluate(f, MethodReplay); !errors.Is(err, ErrUnsafeCounterfactual) {
		t.Fatalf("destructive replay=%v", err)
	}
	c := Counterfactual{Factual: cfFactual(learning.OutcomeVerifiedComplete, StatusUnknown), AlternateOutcome: learning.OutcomeVerifiedComplete, AlternateVerifier: StatusPass}
	if got := Compare(c); got != VerdictUnknown {
		t.Fatalf("UNKNOWN verifier became %s", got)
	}
}

func TestAdversarialEvidenceCannotBeCherryPickedOrSelfReinforced(t *testing.T) {
	results := []ExperimentResult{
		{ID: "win", TaskID: "a", TaskClass: "code", Outcome: StatusPass, ClusterID: "same"},
		{ID: "loss", TaskID: "b", TaskClass: "code", Outcome: StatusFail, Regression: true, ClusterID: "same"},
	}
	base := Baseline{ID: "b", MarshalSHA: "sha", RoutingConfig: "r", VerifierPolicy: "v", Toolchain: "t", EnvironmentHash: "e"}
	c := adversarialCandidate()
	record, _ := Promote(PromotionInput{Candidate: c, Baseline: base, CandidateBaseline: base, Results: results, Reproducible: true}, Governance{MinEvidenceClusters: 2}, time.Now())
	if record.Decision != DecisionReject {
		t.Fatalf("regression cherry-picked into %s", record.Decision)
	}
	f := Feedback{CycleID: "o", CandidateID: "c", SourceCluster: "same", Versions: map[string]string{"sha": "x"}}
	if !SelfReinforcing(f, []string{"same"}) {
		t.Fatal("same-source feedback looked independent")
	}
}

func TestAdversarialStaleHoldoutAndTamperNeverPromote(t *testing.T) {
	base := Baseline{ID: "b", MarshalSHA: "sha", RoutingConfig: "r", VerifierPolicy: "v", Toolchain: "t", EnvironmentHash: "e"}
	c := adversarialCandidate()
	pass := ExperimentResult{ID: "p", TaskID: "p", TaskClass: "code", Outcome: StatusPass, ClusterID: "c1"}
	holdout := ExperimentResult{ID: "h", TaskID: "h", TaskClass: "code", Outcome: StatusFail, Regression: true, ClusterID: "c2", Holdout: true}
	record, _ := Promote(PromotionInput{Candidate: c, Baseline: base, CandidateBaseline: base, Results: []ExperimentResult{pass}, HoldoutResults: []ExperimentResult{holdout}, Reproducible: true}, Governance{MinEvidenceClusters: 1}, time.Now())
	if record.Decision != DecisionReject {
		t.Fatalf("holdout regression promoted: %s", record.Decision)
	}
	record, _ = Promote(PromotionInput{Candidate: c, Baseline: base, CandidateBaseline: base, Results: []ExperimentResult{pass}, Reproducible: true, EvidenceAge: 2 * time.Hour}, Governance{MinEvidenceClusters: 1, EvidenceFreshness: time.Hour}, time.Now())
	if record.Decision != DecisionStale {
		t.Fatalf("stale evidence promoted: %s", record.Decision)
	}
	sealed, err := NewManifest(BenchmarkManifest{ID: "m", Kind: BenchmarkTerminal, Version: "v", EvaluatorVersion: "e", DatasetSnapshot: "d", TaskIDs: []string{"t"}, MarshalSHA: "sha", ConfigDigest: "c", EnvironmentImage: "env"})
	if err != nil {
		t.Fatal(err)
	}
	sealed.MarshalSHA = "other"
	if err := sealed.Verify(); !errors.Is(err, ErrTampered) {
		t.Fatalf("tampered result=%v", err)
	}
}

func TestAdversarialShadowAndExplorationFailClosed(t *testing.T) {
	if err := ValidateShadow(ShadowPolicy{MaxCostMicros: 1, EligibleTaskClasses: []string{"code"}, SecretAccess: true}, "code"); !errors.Is(err, ErrVeto) {
		t.Fatalf("secret shadow=%v", err)
	}
	if err := AllowExploration(ExplorationPolicy{SafeTaskClasses: []string{"code"}, MaxCostMicros: 1, AllowDestructiveEffects: true}, "code", false, 0); !errors.Is(err, ErrVeto) {
		t.Fatalf("destructive exploration=%v", err)
	}
}
