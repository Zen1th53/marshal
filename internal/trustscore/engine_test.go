package trustscore

import (
	"context"
	"strings"
	"testing"
)

func TestEvaluatorComputeScoreWeightedAndEligible(t *testing.T) {
	ev := NewEvaluatorWithPolicy("sha256:policy-prod-v1")
	ctx := context.Background()

	comps := []Component{
		{Name: "quorum", Score: 80.0, Weight: 1.0, HardFail: true},
		{Name: "coverage", Score: 100.0, Weight: 3.0, HardFail: false},
	}
	res, err := ev.ComputeScore(ctx, "sha256:change1", comps)
	if err != nil {
		t.Fatalf("ComputeScore: %v", err)
	}
	if !res.Eligible {
		t.Fatalf("expected Eligible = true")
	}
	// (80*1 + 100*3) / 4 = 380 / 4 = 95.0
	if res.Overall != 95.0 {
		t.Fatalf("expected Overall = 95.0, got %f", res.Overall)
	}
	if res.PolicyDigest != "sha256:policy-prod-v1" {
		t.Fatalf("expected caller policy digest, got %s", res.PolicyDigest)
	}
	if len(res.Reasons) != 1 || res.Reasons[0] != "all hard-fail components passed threshold" {
		t.Fatalf("expected passing reason, got %v", res.Reasons)
	}
}

func TestEvaluatorCeilingAndHardFail(t *testing.T) {
	ev := NewEvaluator()
	ctx := context.Background()

	comps := []Component{
		{Name: "security", Score: 40.0, Weight: 1.0, HardFail: true},
		{Name: "perf", Score: 90.0, Weight: 1.0, Ceiling: 75.0},
	}
	res, err := ev.ComputeScore(ctx, "sha256:change2", comps)
	if err != nil {
		t.Fatalf("ComputeScore: %v", err)
	}
	if res.Eligible {
		t.Fatalf("expected Eligible = false due to hard-fail security component")
	}
	// (40 + 90) / 2 = 65.0. Ceiling is 75.0, so 65 <= 75.
	if res.Overall != 65.0 {
		t.Fatalf("expected Overall = 65.0, got %f", res.Overall)
	}
	// Check that reasons records the hard fail and not passing
	foundFailReason := false
	for _, r := range res.Reasons {
		if strings.Contains(r, "hard-fail component \"security\" below threshold") {
			foundFailReason = true
		}
		if r == "all hard-fail components passed threshold" {
			t.Fatalf("unexpected 'all hard-fail passed' reason when security failed")
		}
	}
	if !foundFailReason {
		t.Fatalf("expected hard-fail explanation in reasons: %v", res.Reasons)
	}
}

func TestEvaluatorCeilingCapping(t *testing.T) {
	ev := NewEvaluator()
	ctx := context.Background()

	comps := []Component{
		{Name: "c1", Score: 95.0, Weight: 1.0, Ceiling: 80.0},
	}
	res, err := ev.ComputeScore(ctx, "sha256:change3", comps)
	if err != nil {
		t.Fatalf("ComputeScore: %v", err)
	}
	if res.Overall != 80.0 {
		t.Fatalf("expected Overall capped at 80.0, got %f", res.Overall)
	}
}
