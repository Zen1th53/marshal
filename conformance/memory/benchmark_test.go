package memory_test

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/conformance/memory"
)

func TestT133CodingMemoryBenchmark(t *testing.T) {
	runner := memory.NewBenchmarkRunner()
	ctx := context.Background()

	report, err := runner.RunBenchmark(ctx)
	if err != nil {
		t.Fatalf("RunBenchmark: %v", err)
	}

	if report.TotalScenarios == 0 {
		t.Fatal("expected non-zero benchmark scenarios")
	}

	if report.HybridMetrics.RecallAtK < 0.8 {
		t.Fatalf("expected Recall@k >= 0.8 on coding benchmark, got: %f", report.HybridMetrics.RecallAtK)
	}

	if report.HybridMetrics.ACLIsolationRate != 1.0 {
		t.Fatalf("expected 100%% ACL isolation rate, got: %f", report.HybridMetrics.ACLIsolationRate)
	}

	if report.HybridMetrics.MRR <= report.LexicalMetrics.MRR {
		t.Fatalf("expected hybrid MRR (%f) > lexical MRR (%f)", report.HybridMetrics.MRR, report.LexicalMetrics.MRR)
	}
}
