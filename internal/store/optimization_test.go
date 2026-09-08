package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/learning"
	"github.com/Zen1th53/marshal/internal/optimization"
)

func p08Entry() optimization.Entry {
	return optimization.Entry{
		ProjectID:       "proj-1",
		MemoryCommitID:  "mc-1",
		MemoryVersion:   1,
		MemoryDigest:    "digest-mc-1",
		SourceSHA:       "sha-1",
		TreeDigest:      "tree-1",
		EnvironmentHash: "env-1",
		Outcome:         learning.OutcomeVerifiedComplete,
	}
}

func p08Candidate(id string, dim optimization.Dimension) optimization.Candidate {
	return optimization.Candidate{
		ID:         id,
		Dimension:  dim,
		Hypothesis: "switch model to claude-3-5-sonnet",
		TaskScope:  []string{"coding"},
		Evidence: []learning.EvidenceRef{
			{ID: "e1", ClusterID: "c1", Digest: "d1", Kind: "benchmark", Observed: time.Now().UTC()},
		},
		RollbackPlan:     "revert model",
		VerificationPlan: "run unit tests",
		Provenance:       "test",
		ClusterID:        "cluster-1",
		Effects: optimization.Effects{
			WeakensApprovals: false,
		},
	}
}

func p08Cycle(t *testing.T, id string, candidates []optimization.Candidate) optimization.Cycle {
	t.Helper()
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	c, err := optimization.NewCycle(optimization.Cycle{
		ID:         id,
		Binding:    p08Entry(),
		Objectives: []optimization.Objective{{Name: "verified_success", HigherIsBetter: true, Weight: 1.0}},
		Candidates: candidates,
		Provenance: "test",
	}, now)
	if err != nil {
		t.Fatalf("NewCycle: %v", err)
	}
	return c
}

func TestOptimizationCycleRoundTripAndDigestVerification(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	c := p08Cycle(t, "opt-1", []optimization.Candidate{p08Candidate("cand-1", optimization.DimRouting)})
	if err := st.AppendOptimizationCycle(ctx, c); err != nil {
		t.Fatalf("AppendOptimizationCycle: %v", err)
	}

	got, err := st.GetOptimizationCycle(ctx, "opt-1")
	if err != nil {
		t.Fatalf("GetOptimizationCycle: %v", err)
	}
	if got.Digest != c.Digest || len(got.Candidates) != 1 {
		t.Fatalf("round trip lost content: %+v", got)
	}

	candidates, err := st.OptimizationCandidates(ctx, "opt-1")
	if err != nil {
		t.Fatalf("OptimizationCandidates: %v", err)
	}
	if len(candidates) != 1 || candidates[0].ID != "cand-1" {
		t.Fatalf("unexpected candidates: %+v", candidates)
	}
}

func TestOptimizationCycleCASUpdate(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	c := p08Cycle(t, "opt-cas", []optimization.Candidate{p08Candidate("cand-1", optimization.DimRouting)})
	if err := st.AppendOptimizationCycle(ctx, c); err != nil {
		t.Fatalf("AppendOptimizationCycle: %v", err)
	}

	// Successful CAS from version 1 to 2
	c2 := c
	c2.Version = 2
	c2.Candidates = append(c2.Candidates, p08Candidate("cand-2", optimization.DimCascade))
	c2Built, err := optimization.NewCycle(c2, time.Now().UTC())
	if err != nil {
		t.Fatalf("NewCycle c2: %v", err)
	}
	if err := st.UpdateOptimizationCycle(ctx, c2Built, 1); err != nil {
		t.Fatalf("UpdateOptimizationCycle: %v", err)
	}

	// Conflicting CAS: expected version 1 should now fail because current version is 2
	cStale := c
	cStale.Version = 2
	cStaleBuilt, err := optimization.NewCycle(cStale, time.Now().UTC())
	if err != nil {
		t.Fatalf("NewCycle cStale: %v", err)
	}
	if err := st.UpdateOptimizationCycle(ctx, cStaleBuilt, 1); !errors.Is(err, optimization.ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
}

func TestOptimizationSubRecords(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	c := p08Cycle(t, "opt-sub", []optimization.Candidate{p08Candidate("cand-1", optimization.DimRouting)})
	if err := st.AppendOptimizationCycle(ctx, c); err != nil {
		t.Fatalf("AppendOptimizationCycle: %v", err)
	}

	now := time.Now().UTC()
	gov := optimization.Governance{
		GovernableProviders: map[string]bool{"codex": true, "claude": true},
	}

	route := optimization.Route{
		TaskClass:       "coding",
		Provider:        "claude",
		ProviderVersion: "3.5",
		Model:           "sonnet",
		Harness:         "custom",
		HarnessVersion:  "1.0",
		VerifierPolicy:  "standard",
	}

	factual := optimization.FactualRun{
		TaskID:            "task-1",
		Route:             route,
		Outcome:           learning.OutcomeVerifiedComplete,
		VerifierResult:    optimization.StatusPass,
		ReplayClass:       learning.ReplayExact,
		TreeDigest:        "tree-1",
		EnvironmentDigest: "env-1",
		ObservedAt:        now,
	}

	// 1. Counterfactual
	cf := optimization.Counterfactual{
		ID:                "cf-1",
		Method:            optimization.MethodReplay,
		Factual:           factual,
		Alternate:         route,
		AlternateOutcome:  learning.OutcomeVerifiedComplete,
		AlternateVerifier: optimization.StatusPass,
		Sandbox: optimization.SandboxPolicy{
			WritableRoot:   "/tmp/sandbox",
			MaxWallMillis:  10000,
			MaxMemoryBytes: 1 << 20,
		},
		ClusterID:   "c1",
		Provenance:  "test",
		EvaluatedAt: now,
	}
	cfBuilt, err := optimization.NewCounterfactual(cf, gov, now)
	if err != nil {
		t.Fatalf("NewCounterfactual: %v", err)
	}
	if err := st.AppendCounterfactual(ctx, "opt-sub", cfBuilt); err != nil {
		t.Fatalf("AppendCounterfactual: %v", err)
	}

	cfs, err := st.Counterfactuals(ctx, "opt-sub")
	if err != nil {
		t.Fatalf("Counterfactuals: %v", err)
	}
	if len(cfs) != 1 || cfs[0].ID != "cf-1" {
		t.Fatalf("unexpected counterfactuals: %+v", cfs)
	}

	// 2. Experiment Result
	res := optimization.ExperimentResult{
		ID:         "res-1",
		TaskID:     "task-1",
		TaskClass:  "coding",
		Outcome:    optimization.StatusPass,
		ObservedAt: now,
	}
	if err := st.AppendExperimentResult(ctx, "opt-sub", "cand-1", res); err != nil {
		t.Fatalf("AppendExperimentResult: %v", err)
	}
	results, err := st.ExperimentResults(ctx, "opt-sub", "cand-1")
	if err != nil {
		t.Fatalf("ExperimentResults: %v", err)
	}
	if len(results) != 1 || results[0].ID != "res-1" {
		t.Fatalf("unexpected results: %+v", results)
	}

	// 3. Canary
	canary := optimization.Canary{
		ID:                  "canary-1",
		CandidateID:         "cand-1",
		BaselineID:          "base-1",
		Exposure:            0.1,
		EligibleTaskClasses: []string{"coding"},
		MaxTasks:            10,
		Deadline:            now.Add(time.Hour),
		RollbackTriggers: []optimization.Trigger{
			{Metric: "error_rate", Threshold: 0.05, HigherIsWorse: true, Reason: "error spike"},
		},
		StartedAt: now,
		State:               optimization.CanaryPending,
	}
	if err := st.AppendCanary(ctx, "opt-sub", "", canary); err != nil {
		t.Fatalf("AppendCanary: %v", err)
	}
	canaries, err := st.Canaries(ctx, "opt-sub")
	if err != nil {
		t.Fatalf("Canaries: %v", err)
	}
	if len(canaries) != 1 || canaries[0].ID != "canary-1" {
		t.Fatalf("unexpected canaries: %+v", canaries)
	}
}

func TestOptimizationConcurrentCycleAppends(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			cycleID := "opt-concurrent-" + string(rune('a'+idx))
			c := p08Cycle(t, cycleID, []optimization.Candidate{p08Candidate("cand-"+cycleID, optimization.DimRouting)})
			_ = st.AppendOptimizationCycle(ctx, c)
		}(i)
	}
	wg.Wait()
}
