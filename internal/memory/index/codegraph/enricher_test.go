package codegraph_test

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/index/codegraph"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT107CodeGraphEnrichmentAndImpactTraversal(t *testing.T) {
	enricher := codegraph.NewEnricher()
	ctx := context.Background()

	// 1. Link memory to file & symbol
	rec := model.MemoryRecordV2{
		ID:         "MEM-DEC-10",
		ProjectID:  "PROJ-1",
		Kind:       model.MemoryKindDecision,
		Lifecycle:  model.MemoryDurable,
		Title:      "Concurrency Safety in Store",
		HeadCommit: "commit-c1",
		ExtMeta: map[string]any{
			"touched_files":   []string{"internal/store/memory.go"},
			"touched_symbols": []string{"WriteMemoryV2", "UpdateMemory"},
		},
	}

	if err := enricher.EnrichRecord(ctx, rec); err != nil {
		t.Fatalf("EnrichRecord: %v", err)
	}

	// 2. Query impact for symbol "WriteMemoryV2"
	symbolImpact, err := enricher.FindImpact(ctx, "PROJ-1", "internal/store/memory.go", "WriteMemoryV2", "commit-c1")
	if err != nil {
		t.Fatalf("FindImpact by symbol: %v", err)
	}
	if len(symbolImpact) != 1 || symbolImpact[0].MemoryID != "MEM-DEC-10" {
		t.Fatalf("expected impact to return MEM-DEC-10, got: %+v", symbolImpact)
	}

	// 3. Commit drift flags link as stale
	staleImpact, err := enricher.FindImpact(ctx, "PROJ-1", "internal/store/memory.go", "WriteMemoryV2", "commit-c2-diverged")
	if err != nil {
		t.Fatalf("FindImpact with diverged commit: %v", err)
	}
	if len(staleImpact) != 1 || !staleImpact[0].IsStale {
		t.Fatalf("expected impact link to be marked IsStale due to commit divergence, got: %+v", staleImpact)
	}

	// 4. File rename handling
	enricher.RecordFileRename("internal/store/memory.go", "internal/store/memory_v2.go")
	renamedImpact, err := enricher.FindImpact(ctx, "PROJ-1", "internal/store/memory_v2.go", "WriteMemoryV2", "commit-c1")
	if err != nil {
		t.Fatalf("FindImpact on renamed file: %v", err)
	}
	if len(renamedImpact) != 1 || renamedImpact[0].MemoryID != "MEM-DEC-10" {
		t.Fatalf("expected renamed file lookup to find MEM-DEC-10, got: %+v", renamedImpact)
	}
}
