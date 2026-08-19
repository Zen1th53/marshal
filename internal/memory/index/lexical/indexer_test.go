package lexical_test

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/index/lexical"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT101LexicalExactRetrievalAndBoosts(t *testing.T) {
	idx := lexical.NewLexicalIndex()
	ctx := context.Background()
	now := time.Now().UTC()

	// 1. Ingest records: one with exact path & symbol, one with general text
	rec1 := model.MemoryRecordV2{
		ID:         "MEM-PATH-01",
		ProjectID:  "PROJ-1",
		Kind:       model.MemoryKindSemantic,
		Lifecycle:  model.MemoryDurable,
		Title:      "Store Memory Writer",
		Body:       "The function WriteMemoryV2 in internal/store/memory.go writes canonical memory.",
		ObservedAt: now,
		CreatedAt:  now,
	}

	rec2 := model.MemoryRecordV2{
		ID:         "MEM-GEN-02",
		ProjectID:  "PROJ-1",
		Kind:       model.MemoryKindSemantic,
		Lifecycle:  model.MemoryDurable,
		Title:      "General Documentation",
		Body:       "We use Go for implementing core memory structures.",
		ObservedAt: now,
		CreatedAt:  now,
	}

	if err := idx.IndexRecord(ctx, rec1); err != nil {
		t.Fatalf("IndexRecord 1: %v", err)
	}
	if err := idx.IndexRecord(ctx, rec2); err != nil {
		t.Fatalf("IndexRecord 2: %v", err)
	}

	// 2. Query exact symbol "WriteMemoryV2"
	results, err := idx.Search(ctx, "PROJ-1", "WriteMemoryV2", 10)
	if err != nil {
		t.Fatalf("Search exact symbol: %v", err)
	}
	if len(results) == 0 || results[0].MemoryID != "MEM-PATH-01" {
		t.Fatalf("expected top result to be MEM-PATH-01 for exact symbol, got: %+v", results)
	}
	if results[0].Score <= 0 {
		t.Fatalf("expected positive score, got: %f", results[0].Score)
	}

	// 3. Query exact file path "internal/store/memory.go"
	pathResults, err := idx.Search(ctx, "PROJ-1", "internal/store/memory.go", 10)
	if err != nil {
		t.Fatalf("Search file path: %v", err)
	}
	if len(pathResults) == 0 || pathResults[0].MemoryID != "MEM-PATH-01" {
		t.Fatalf("expected top result to be MEM-PATH-01 for exact path, got: %+v", pathResults)
	}

	// 4. Tombstone / Delete removal
	if err := idx.RemoveRecord(ctx, "MEM-PATH-01"); err != nil {
		t.Fatalf("RemoveRecord: %v", err)
	}

	afterDeleteResults, err := idx.Search(ctx, "PROJ-1", "WriteMemoryV2", 10)
	if err != nil {
		t.Fatalf("Search after delete: %v", err)
	}
	if len(afterDeleteResults) != 0 {
		t.Fatalf("expected 0 results after removing record, got %d", len(afterDeleteResults))
	}

	// 5. Rebuild index parity
	if err := idx.Rebuild(ctx, []model.MemoryRecordV2{rec1, rec2}); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	rebuildResults, err := idx.Search(ctx, "PROJ-1", "WriteMemoryV2", 10)
	if err != nil {
		t.Fatalf("Search after rebuild: %v", err)
	}
	if len(rebuildResults) == 0 || rebuildResults[0].MemoryID != "MEM-PATH-01" {
		t.Fatalf("expected rebuilt index to restore MEM-PATH-01, got: %+v", rebuildResults)
	}
}
