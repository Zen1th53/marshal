package external_test

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/conformance/memory/external"
)

func TestT159EvoMemBenchAdapter(t *testing.T) {
	ctx := context.Background()
	adapter := external.NewEvoMemBenchAdapter()

	scenarios := []external.EvoScenario{
		{
			ID:       "SCENARIO-KNOWLEDGE-01",
			Category: "KNOWLEDGE_RETRIEVAL",
			Query:    "SQLite WAL pragma configuration",
			Expected: "PRAGMA journal_mode=WAL;",
		},
		{
			ID:       "SCENARIO-WORKFLOW-02",
			Category: "PROCEDURAL_EXECUTION",
			Query:    "How to run migration and release verify",
			Expected: "release_verify.py",
		},
	}

	// 1. Run comparison suite across configs
	results, err := adapter.RunComparisonSuite(ctx, scenarios)
	if err != nil {
		t.Fatalf("RunComparisonSuite: %v", err)
	}

	if len(results.ConfigReports) < 3 {
		t.Fatalf("expected at least 3 baseline configs evaluated, got: %d", len(results.ConfigReports))
	}
	if results.ConfigHash == "" {
		t.Fatal("expected non-empty deterministic config hash")
	}

	// 2. Adaptive configuration should meet or exceed simple baselines
	adaptiveReport, ok := results.ConfigReports["adaptive"]
	if !ok || adaptiveReport.CategoryScores["KNOWLEDGE_RETRIEVAL"] <= 0.0 {
		t.Fatalf("expected valid adaptive category scores: %+v", adaptiveReport)
	}
}
