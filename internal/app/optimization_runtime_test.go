package app

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/learning"
	"github.com/Zen1th53/marshal/internal/optimization"
	"github.com/Zen1th53/marshal/internal/verification"
)

type e2eReplayRunner struct{ called bool }

func (r *e2eReplayRunner) Replay(context.Context, optimization.ReplayRequest) (optimization.ReplayObservation, error) {
	r.called = true
	return optimization.ReplayObservation{Outcome: learning.OutcomeVerifiedComplete, VerifierResult: optimization.StatusPass}, nil
}

// This is the durable P06→P07→P08 segment of the full lifecycle. P06 creates
// the attestation, P07 canonically commits the learning, and P08 derives its
// binding rather than accepting it from a caller, executes a real replay, and
// preserves the rollback evidence.
func TestOptimizationLifecycleFromVerifiedLearningThroughReplayAndRollback(t *testing.T) {
	ctx := context.Background()
	runtime, session := p07Runtime(t, verification.VerifiedComplete)
	commit, err := runtime.Learning().Commit(ctx, CommitInput{ID: "mc-p08", Verification: session.ID, Provenance: "e2e", Candidates: []learning.PromotionInput{p07Candidate("memory-p08", "bounded routing observation")}})
	if err != nil {
		t.Fatal(err)
	}
	service := runtime.Optimization()
	baseline := optimization.Baseline{ID: "base", MarshalSHA: "sha", RoutingConfig: "route", VerifierPolicy: "verify", Toolchain: "tool", EnvironmentHash: "env"}
	candidate := optimization.Candidate{ID: "candidate", Dimension: optimization.DimRouting, Hypothesis: "evaluate alternate governed route", TaskScope: []string{"code"}, RollbackPlan: "restore baseline", VerificationPlan: "independent verifier", Provenance: "e2e"}
	gov := optimization.Governance{GovernableProviders: map[string]bool{"codex": true, "claude": true}, MaxCanaryExposure: .25, RequireRollback: true}
	cycle, err := service.StartCycle(ctx, StartCycleInput{ID: "opt-e2e", MemoryCommitID: commit.ID, Objectives: []optimization.Objective{{Name: "verified_success", HigherIsBetter: true, Weight: 1}}, Candidates: []optimization.Candidate{candidate}, Baselines: []optimization.Baseline{baseline}, Provenance: "e2e", Governance: gov})
	if err != nil {
		t.Fatal(err)
	}
	if cycle.Binding.MemoryCommitID != commit.ID {
		t.Fatalf("cycle did not bind canonical Process 07 commit: %+v", cycle.Binding)
	}
	listed, err := service.List(ctx)
	if err != nil || len(listed) != 1 || listed[0].ID != cycle.ID {
		t.Fatalf("listed optimization cycles = %+v, err=%v", listed, err)
	}
	factual := optimization.FactualRun{TaskID: "task", Route: optimization.Route{TaskClass: "code", Provider: "codex", ProviderVersion: "1", Model: "m", Harness: "h", HarnessVersion: "1", VerifierPolicy: "verify"}, Outcome: learning.OutcomeFailed, VerifierResult: optimization.StatusFail, ReplayClass: learning.ReplayExact, TreeDigest: "tree", EnvironmentDigest: "env"}
	runner := &e2eReplayRunner{}
	cf, err := service.ExecuteReplay(ctx, cycle.ID, runner, factual, optimization.Route{TaskClass: "code", Provider: "claude", ProviderVersion: "1", Model: "m", Harness: "h", HarnessVersion: "1", VerifierPolicy: "verify"}, optimization.SandboxPolicy{WritableRoot: t.TempDir(), MaxWallMillis: 1_000, MaxMemoryBytes: 1 << 20}, "e2e", "cluster-1", gov)
	if err != nil {
		t.Fatal(err)
	}
	if !runner.called || cf.AlternateVerifier != optimization.StatusPass {
		t.Fatalf("replay=%+v called=%t", cf, runner.called)
	}
	canary := optimization.Canary{ID: "canary", CandidateID: candidate.ID, BaselineID: baseline.ID, Exposure: .1, EligibleTaskClasses: []string{"code"}, MaxTasks: 2, Deadline: time.Now().Add(time.Hour), RollbackTriggers: []optimization.Trigger{{Metric: "error_rate", Threshold: .05, HigherIsWorse: true, Reason: "regression"}}}
	opened, err := service.Canary(ctx, cycle.ID, "", canary, gov)
	if err != nil {
		t.Fatal(err)
	}
	active, err := service.ActiveCanaries(ctx)
	if err != nil || len(active) != 1 || active[0].ID != opened.ID {
		t.Fatalf("active canaries before rollback = %+v, err=%v", active, err)
	}
	rolled, err := service.Rollback(ctx, opened.ID, "e2e regression")
	if err != nil {
		t.Fatal(err)
	}
	if rolled.State != optimization.CanaryRolledBack || rolled.RollbackReason == "" {
		t.Fatalf("rollback=%+v", rolled)
	}
	// Rollback must preserve the optimization parent binding. The former
	// update path replaced it with an empty cycle id, making the record vanish
	// from its own evidence chain after a successful rollback.
	bound, err := service.Canaries(ctx, cycle.ID)
	if err != nil || len(bound) != 1 || bound[0].State != optimization.CanaryRolledBack {
		t.Fatalf("cycle canaries after rollback = %+v, err=%v", bound, err)
	}
	active, err = service.ActiveCanaries(ctx)
	if err != nil || len(active) != 0 {
		t.Fatalf("active canaries after rollback = %+v, err=%v", active, err)
	}
}
