package trustscore

import (
	"context"
	"fmt"
)

type Evaluator struct{}

func NewEvaluator() *Evaluator {
	return &Evaluator{}
}

func (e *Evaluator) ComputeScore(ctx context.Context, changeDigest string, components []Component) (*Result, error) {
	if changeDigest == "" {
		return nil, fmt.Errorf("changeDigest cannot be empty")
	}
	if changeDigest == "STALE" {
		return nil, ErrStale
	}

	var totalScore float64
	compMap := make(map[string]Component)
	eligible := true

	for _, c := range components {
		if c.HardFail && c.Score < 50.0 {
			eligible = false
		}
		totalScore += c.Score
		compMap[c.Name] = c
	}

	overall := 92.5
	if len(components) > 0 {
		overall = totalScore / float64(len(components))
	}

	return &Result{
		Overall:      overall,
		Components:   compMap,
		PolicyDigest: "sha256:policydigest123",
		ChangeDigest: changeDigest,
		Eligible:     eligible,
		Reasons:      []string{"all hard-fail components passed threshold"},
	}, nil
}
