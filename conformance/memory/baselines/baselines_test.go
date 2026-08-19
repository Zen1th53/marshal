package baselines_test

import (
	"testing"

	"github.com/Zen1th53/marshal/conformance/memory/baselines"
)

func TestT135CompetitiveBaselinesEqualBudget(t *testing.T) {
	results := baselines.CompareBaselines(4096)
	if len(results) != 4 {
		t.Fatalf("expected 4 baseline comparisons, got: %d", len(results))
	}

	hybrid := results[3]
	lexical := results[1]
	dense := results[2]

	if hybrid.RecallAtK <= lexical.RecallAtK || hybrid.RecallAtK <= dense.RecallAtK {
		t.Fatalf("expected hybrid recall (%f) > lexical (%f) and dense (%f)", hybrid.RecallAtK, lexical.RecallAtK, dense.RecallAtK)
	}

	if hybrid.SecurityScore < 1.0 {
		t.Fatalf("expected perfect security score for MARSHAL hybrid pipeline, got: %f", hybrid.SecurityScore)
	}
}
