package evolution

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	ErrSecurityInvariantRegression = errors.New("candidate configuration rejected: caused security invariant or scope leakage violation")
	ErrPerformanceRegression       = errors.New("candidate configuration rejected: recall or accuracy regressed below baseline")
)

type CandidateConfig struct {
	ConfigID                string  `json:"config_id"`
	LexicalWeight           float64 `json:"lexical_weight"`
	VectorWeight            float64 `json:"vector_weight"`
	ScopeViolationsDetected int     `json:"scope_violations_detected"`
	RecallScore             float64 `json:"recall_score"`
}

type CanaryReport struct {
	ConfigID          string    `json:"config_id"`
	ApprovedForCanary bool      `json:"approved_for_canary"`
	BaselineRecall    float64   `json:"baseline_recall"`
	CandidateRecall   float64   `json:"candidate_recall"`
	DeltaRecall       float64   `json:"delta_recall"`
	EvaluatedAt       time.Time `json:"evaluated_at"`
}

type CanaryEvaluator struct{}

func NewCanaryEvaluator() *CanaryEvaluator {
	return &CanaryEvaluator{}
}

// EvaluateCandidate strictly validates candidate configuration against security invariants and performance baselines.
func (e *CanaryEvaluator) EvaluateCandidate(ctx context.Context, candidate CandidateConfig, baselineRecall float64) (CanaryReport, error) {
	// Rule 1: Hard security invariant constraint (ZERO tolerance for scope leaks)
	if candidate.ScopeViolationsDetected > 0 {
		return CanaryReport{
			ConfigID:          candidate.ConfigID,
			ApprovedForCanary: false,
			EvaluatedAt:       time.Now().UTC(),
		}, fmt.Errorf("%w: detected %d scope leak violations", ErrSecurityInvariantRegression, candidate.ScopeViolationsDetected)
	}

	// Rule 2: Performance regression constraint
	if candidate.RecallScore < baselineRecall {
		return CanaryReport{
			ConfigID:          candidate.ConfigID,
			ApprovedForCanary: false,
			BaselineRecall:    baselineRecall,
			CandidateRecall:   candidate.RecallScore,
			DeltaRecall:       candidate.RecallScore - baselineRecall,
			EvaluatedAt:       time.Now().UTC(),
		}, fmt.Errorf("%w: score %f < baseline %f", ErrPerformanceRegression, candidate.RecallScore, baselineRecall)
	}

	return CanaryReport{
		ConfigID:          candidate.ConfigID,
		ApprovedForCanary: true,
		BaselineRecall:    baselineRecall,
		CandidateRecall:   candidate.RecallScore,
		DeltaRecall:       candidate.RecallScore - baselineRecall,
		EvaluatedAt:       time.Now().UTC(),
	}, nil
}
