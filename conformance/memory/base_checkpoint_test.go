package memory_test

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/conformance/memory"
	"github.com/Zen1th53/marshal/conformance/memory/adversarial"
	"github.com/Zen1th53/marshal/internal/memory/index/lexical"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT136BaseMemoryConformanceCheckpoint(t *testing.T) {
	ctx := context.Background()

	// 1. Run Benchmark
	benchRunner := memory.NewBenchmarkRunner()
	benchReport, err := benchRunner.RunBenchmark(ctx)
	if err != nil {
		t.Fatalf("RunBenchmark: %v", err)
	}

	// 2. Run Adversarial Suite
	advRunner := adversarial.NewSuiteRunner()
	advReport, err := advRunner.RunAdversarialSuite(ctx)
	if err != nil {
		t.Fatalf("RunAdversarialSuite: %v", err)
	}
	if !advReport.AllPassed {
		t.Fatalf("Adversarial suite failed: %+v", advReport)
	}

	// 3. Rebuild Parity Verification
	rec := model.MemoryRecordV2{
		ID:        "MEM-REBUILD-01",
		ProjectID: "PROJ-1",
		Title:     "SQLite WAL Pragma",
		Body:      "PRAGMA journal_mode=WAL;",
		Lifecycle: model.MemoryDurable,
	}
	lexIdx := lexical.NewLexicalIndex()
	_ = lexIdx.IndexRecord(ctx, rec)

	// Rebuild into new index
	rebuiltIdx := lexical.NewLexicalIndex()
	_ = rebuiltIdx.IndexRecord(ctx, rec)

	resOriginal, _ := lexIdx.Search(ctx, "PROJ-1", "WAL", 10)
	resRebuilt, _ := rebuiltIdx.Search(ctx, "PROJ-1", "WAL", 10)
	if len(resOriginal) != len(resRebuilt) || len(resOriginal) == 0 {
		t.Fatal("rebuild parity failure between original and rebuilt index")
	}

	if benchReport.HybridMetrics.RecallAtK == 0 || !advReport.AllPassed {
		t.Fatal("conformance validation failed")
	}
}
