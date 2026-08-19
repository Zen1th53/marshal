package turbovec_test

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/index/vector/turbovec"
)

func TestT104TurboVecOptionalBackend(t *testing.T) {
	backend := turbovec.NewBackend(turbovec.Config{
		Enabled: true,
	})
	ctx := context.Background()

	// 1. Health check
	if err := backend.Health(ctx); err != nil {
		t.Fatalf("Health check failed: %v", err)
	}

	// 2. Upsert vectors
	emb1 := []float32{1.0, 0.0, 0.0}
	emb2 := []float32{0.0, 1.0, 0.0}

	if err := backend.UpsertVector(ctx, "MEM-TV-1", "PROJ-1", "scope-1", emb1); err != nil {
		t.Fatalf("UpsertVector 1: %v", err)
	}
	if err := backend.UpsertVector(ctx, "MEM-TV-2", "PROJ-1", "scope-2", emb2); err != nil {
		t.Fatalf("UpsertVector 2: %v", err)
	}

	// 3. Search with filtered scope
	query := []float32{1.0, 0.2, 0.0}
	results, err := backend.SearchVectors(ctx, "PROJ-1", []string{"scope-1"}, query, 10)
	if err != nil {
		t.Fatalf("SearchVectors: %v", err)
	}
	if len(results) != 1 || results[0].MemoryID != "MEM-TV-1" {
		t.Fatalf("expected filtered result MEM-TV-1, got: %+v", results)
	}

	// 4. Crash / Restart simulation
	backend.SimulateCrash()
	if err := backend.Health(ctx); err != nil {
		t.Fatalf("Health check failed after crash recovery: %v", err)
	}
}
