package evolution_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/evolution"
)

func TestT158ShadowEvaluationAndCanaryRollout(t *testing.T) {
	ctx := context.Background()
	evaluator := evolution.NewCanaryEvaluator()

	// 1. Candidate config improves speed/metric but causes a security scope leak -> MUST BE REJECTED
	leakyCandidate := evolution.CandidateConfig{
		ConfigID:      "CFG-LEAKY-01",
		LexicalWeight: 0.8,
		VectorWeight:  0.2,
		ScopeViolationsDetected: 1, // Leaked cross-tenant memory in shadow eval
		RecallScore:   0.98,
	}

	report, err := evaluator.EvaluateCandidate(ctx, leakyCandidate, 0.90)
	if !errors.Is(err, evolution.ErrSecurityInvariantRegression) {
		t.Fatalf("expected ErrSecurityInvariantRegression for candidate with scope violation, got: %v (report: %+v)", err, report)
	}

	// 2. Candidate regresses recall metric below baseline -> Auto-revert
	regressedCandidate := evolution.CandidateConfig{
		ConfigID:                "CFG-REGRESSED-02",
		LexicalWeight:           0.1,
		VectorWeight:            0.9,
		ScopeViolationsDetected: 0,
		RecallScore:             0.75, // Below baseline 0.90
	}

	reportRegressed, err := evaluator.EvaluateCandidate(ctx, regressedCandidate, 0.90)
	if !errors.Is(err, evolution.ErrPerformanceRegression) {
		t.Fatalf("expected ErrPerformanceRegression for regressed candidate, got: %v (report: %+v)", err, reportRegressed)
	}

	// 3. Valid candidate improves metric and preserves 0 security violations -> Approved for canary
	validCandidate := evolution.CandidateConfig{
		ConfigID:                "CFG-VALID-03",
		LexicalWeight:           0.5,
		VectorWeight:            0.5,
		ScopeViolationsDetected: 0,
		RecallScore:             0.95,
	}

	approvedReport, err := evaluator.EvaluateCandidate(ctx, validCandidate, 0.90)
	if err != nil || !approvedReport.ApprovedForCanary {
		t.Fatalf("expected valid candidate approved for canary, got: %+v (err: %v)", approvedReport, err)
	}
}
