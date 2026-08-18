package trustscore

import (
	"context"
	"testing"
)

func TestEvaluatorComputeScore(t *testing.T) {
	ev := NewEvaluator()
	ctx := context.Background()

	comps := []Component{{Name: "quorum", Score: 95.0, Weight: 1.0}}
	res, err := ev.ComputeScore(ctx, "sha256:change1", comps)
	if err != nil {
		t.Fatalf("ComputeScore: %v", err)
	}
	if !res.Eligible {
		t.Fatalf("expected Eligible = true")
	}
}
