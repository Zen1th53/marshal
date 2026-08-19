package external_test

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/conformance/memory/external"
)

func TestT161ActionOrientedAndProactiveMemoryBenchmark(t *testing.T) {
	ctx := context.Background()
	runner := external.NewProactiveBenchmarkRunner()

	// 1. Run action-oriented multi-session benchmark
	report, err := runner.RunMultiSessionArena(ctx)
	if err != nil {
		t.Fatalf("RunMultiSessionArena: %v", err)
	}

	// 2. Action success improvement over no-memory baseline
	if report.ActionSuccessRate <= report.BaselineSuccessRate {
		t.Fatalf("expected action success rate (%f) > baseline (%f)", report.ActionSuccessRate, report.BaselineSuccessRate)
	}

	// 3. Proactive recall precision and timing score
	if report.ProactiveTimingScore < 0.90 {
		t.Fatalf("expected proactive timing score >= 0.90, got: %f", report.ProactiveTimingScore)
	}

	// 4. Token efficiency: fewer steps than baseline
	if report.AverageStepsPerTask >= report.BaselineStepsPerTask {
		t.Fatalf("expected reduced step count with memory (%f) < baseline (%f)", report.AverageStepsPerTask, report.BaselineStepsPerTask)
	}

	// 5. Matched manifest digest
	if report.ManifestDigest == "" {
		t.Fatal("expected non-empty deterministic manifest digest")
	}
}
