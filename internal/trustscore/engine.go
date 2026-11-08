package trustscore

import (
	"context"
	"fmt"
)

type Evaluator struct {
	PolicyDigest string
}

func NewEvaluator() *Evaluator {
	return &Evaluator{}
}

func NewEvaluatorWithPolicy(policyDigest string) *Evaluator {
	return &Evaluator{PolicyDigest: policyDigest}
}

func (e *Evaluator) ComputeScore(ctx context.Context, changeDigest string, components []Component) (*Result, error) {
	if changeDigest == "" {
		return nil, fmt.Errorf("changeDigest cannot be empty")
	}
	if len(components) == 0 {
		return nil, ErrNoData
	}

	compMap := make(map[string]Component, len(components))
	var weightedSum float64
	var totalWeight float64
	var minCeiling float64 = -1.0
	eligible := true
	var reasons []string

	for _, c := range components {
		w := c.Weight
		if w <= 0 {
			w = 1.0
		}
		weightedSum += c.Score * w
		totalWeight += w

		if c.Ceiling > 0 {
			if minCeiling < 0 || c.Ceiling < minCeiling {
				minCeiling = c.Ceiling
			}
		}

		if c.HardFail && c.Score < 50.0 {
			eligible = false
			reasons = append(reasons, fmt.Sprintf("hard-fail component %q below threshold (score: %.1f)", c.Name, c.Score))
		}
		compMap[c.Name] = c
	}

	overall := weightedSum / totalWeight
	if minCeiling > 0 && overall > minCeiling {
		overall = minCeiling
	}

	if eligible {
		reasons = append(reasons, "all hard-fail components passed threshold")
	}

	return &Result{
		Overall:      overall,
		Components:   compMap,
		PolicyDigest: e.PolicyDigest,
		ChangeDigest: changeDigest,
		Eligible:     eligible,
		Reasons:      reasons,
	}, nil
}
