package app

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/learning"
	"github.com/Zen1th53/marshal/internal/optimization"
	"github.com/Zen1th53/marshal/internal/verification"
)

// restartCycleFixture builds a durable optimization cycle with one live canary,
// which is the state a crash mid-rollout leaves behind.
func restartCycleFixture(t *testing.T) (*Runtime, string) {
	t.Helper()
	ctx := context.Background()
	runtime, session := p07Runtime(t, verification.VerifiedComplete)
	commit, err := runtime.Learning().Commit(ctx, CommitInput{
		ID: "mc-restart", Verification: session.ID, Provenance: "restart",
		Candidates: []learning.PromotionInput{p07Candidate("memory-restart", "bounded routing observation")},
	})
	if err != nil {
		t.Fatal(err)
	}

	gov := optimization.Governance{
		GovernableProviders: map[string]bool{"codex": true, "claude": true},
		MaxCanaryExposure:   .25, RequireRollback: true,
	}
	candidate := optimization.Candidate{
		ID: "candidate-restart", Dimension: optimization.DimRouting,
		Hypothesis: "evaluate alternate governed route", TaskScope: []string{"code"},
		RollbackPlan: "restore baseline", VerificationPlan: "independent verifier",
		Provenance: "restart",
	}
	baseline := optimization.Baseline{
		ID: "base-restart", MarshalSHA: "sha", RoutingConfig: "route",
		VerifierPolicy: "verify", Toolchain: "tool", EnvironmentHash: "env",
	}
	cycle, err := runtime.Optimization().StartCycle(ctx, StartCycleInput{
		ID: "opt-restart", MemoryCommitID: commit.ID,
		Objectives: []optimization.Objective{{Name: "verified_success", HigherIsBetter: true, Weight: 1}},
		Candidates: []optimization.Candidate{candidate},
		Baselines:  []optimization.Baseline{baseline},
		Provenance: "restart", Governance: gov,
	})
	if err != nil {
		t.Fatal(err)
	}

	// The canary is stored already RUNNING, which is the state a crash
	// mid-rollout leaves behind: live, with nothing evaluating its triggers.
	if _, err := runtime.Optimization().Canary(ctx, cycle.ID, "", optimization.Canary{
		ID: "canary-restart", CandidateID: candidate.ID, BaselineID: baseline.ID,
		State:    optimization.CanaryRunning,
		Exposure: .1, EligibleTaskClasses: []string{"code"}, MaxTasks: 10,
		Deadline: time.Now().UTC().Add(time.Hour),
		RollbackTriggers: []optimization.Trigger{{
			Metric: "error_rate", Threshold: .05, HigherIsWorse: true, Reason: "regression",
		}},
	}, gov); err != nil {
		t.Fatal(err)
	}
	return runtime, cycle.ID
}

// Recovery reloads the cycle from durable state and reconciles the work that
// was in flight. An experiment interrupted before a durable result was written
// is quarantined, never assumed to have passed.
func TestOptimizationRecoverQuarantinesInterruptedWork(t *testing.T) {
	ctx := context.Background()
	runtime, cycleID := restartCycleFixture(t)

	plans, err := runtime.Optimization().Recover(ctx, cycleID, []string{"exp-interrupted"})
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}

	var sawQuarantine, sawHalt bool
	for _, p := range plans {
		if p.Subject == "exp-interrupted" && p.Action == optimization.ResumeQuarantine {
			sawQuarantine = true
		}
		if p.Kind == "canary" && p.Action == optimization.ResumeHaltCanary {
			sawHalt = true
		}
		if p.Kind == "experiment" && p.Action == optimization.ResumeSafe {
			t.Fatalf("interrupted experiment %s would resume as complete", p.Subject)
		}
	}
	if !sawQuarantine {
		t.Fatalf("interrupted work was not quarantined: %+v", plans)
	}
	if !sawHalt {
		t.Fatalf("a canary live across the restart was not halted: %+v", plans)
	}
}

// Recovery is a read: it returns a plan for review rather than applying state
// changes, because automatically resuming is how a half-finished experiment
// quietly becomes a result.
func TestOptimizationRecoverDoesNotMutateState(t *testing.T) {
	ctx := context.Background()
	runtime, cycleID := restartCycleFixture(t)

	before, err := runtime.Optimization().CanaryStatus(ctx, "canary-restart")
	if err != nil {
		t.Fatalf("CanaryStatus: %v", err)
	}
	if _, err := runtime.Optimization().Recover(ctx, cycleID, nil); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	after, err := runtime.Optimization().CanaryStatus(ctx, "canary-restart")
	if err != nil {
		t.Fatalf("CanaryStatus: %v", err)
	}
	if before.State != after.State {
		t.Fatalf("recovery mutated canary state: %s became %s", before.State, after.State)
	}
}

// Recovering a cycle that does not exist fails rather than inventing one.
func TestOptimizationRecoverRefusesUnknownCycle(t *testing.T) {
	ctx := context.Background()
	runtime, _ := restartCycleFixture(t)
	if _, err := runtime.Optimization().Recover(ctx, "opt-does-not-exist", nil); err == nil {
		t.Fatal("recovery of a nonexistent cycle succeeded")
	}
}
