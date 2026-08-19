package vector_test

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/index/vector"
)

func TestT102VectorBackendContract(t *testing.T) {
	store := vector.NewLocalVectorStore()
	ctx := context.Background()

	// 1. Add vector embeddings
	emb1 := []float32{1.0, 0.0, 0.0} // points along X
	emb2 := []float32{0.0, 1.0, 0.0} // points along Y

	if err := store.UpsertVector(ctx, "MEM-VEC-1", "PROJ-1", "scope-main", emb1); err != nil {
		t.Fatalf("UpsertVector 1: %v", err)
	}
	if err := store.UpsertVector(ctx, "MEM-VEC-2", "PROJ-1", "scope-private", emb2); err != nil {
		t.Fatalf("UpsertVector 2: %v", err)
	}

	// 2. Search with query pointing along X
	queryX := []float32{0.9, 0.1, 0.0}
	results, err := store.SearchVectors(ctx, "PROJ-1", []string{"scope-main", "scope-private"}, queryX, 10)
	if err != nil {
		t.Fatalf("SearchVectors: %v", err)
	}
	if len(results) != 2 || results[0].MemoryID != "MEM-VEC-1" {
		t.Fatalf("expected top result MEM-VEC-1, got: %+v", results)
	}

	// 3. Scope allowlist filter: querying with only "scope-main" must hide "MEM-VEC-2"
	scopedResults, err := store.SearchVectors(ctx, "PROJ-1", []string{"scope-main"}, queryX, 10)
	if err != nil {
		t.Fatalf("SearchVectors scoped: %v", err)
	}
	if len(scopedResults) != 1 || scopedResults[0].MemoryID != "MEM-VEC-1" {
		t.Fatalf("expected only 1 scoped result (MEM-VEC-1), got: %+v", scopedResults)
	}

	// 4. Delete vector
	if err := store.DeleteVector(ctx, "MEM-VEC-1"); err != nil {
		t.Fatalf("DeleteVector: %v", err)
	}

	afterDeleteResults, err := store.SearchVectors(ctx, "PROJ-1", []string{"scope-main"}, queryX, 10)
	if err != nil {
		t.Fatalf("Search after delete: %v", err)
	}
	if len(afterDeleteResults) != 0 {
		t.Fatalf("expected 0 results after deletion, got %d", len(afterDeleteResults))
	}
}
