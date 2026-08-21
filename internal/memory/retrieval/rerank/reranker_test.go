package rerank_test

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/retrieval/fusion"
	"github.com/Zen1th53/marshal/internal/memory/retrieval/rerank"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT111RerankingDiversityAndSecurity(t *testing.T) {
	reranker := rerank.NewReranker(rerank.Config{
		SimilarityThreshold: 0.70,
		Timeout:             50 * time.Millisecond,
	})
	ctx := context.Background()

	// 1. Cluster of near-duplicate records (same title/provenance cluster)
	candidates := []fusion.FusedResult{
		{MemoryID: "MEM-DUP-1", RankScore: 0.95},
		{MemoryID: "MEM-DUP-2", RankScore: 0.93},  // Near duplicate of MEM-DUP-1
		{MemoryID: "MEM-DIFF-3", RankScore: 0.80}, // Distinct informative record
	}

	records := map[string]model.MemoryRecordV2{
		"MEM-DUP-1":  {ID: "MEM-DUP-1", Title: "Fix DB lock timeout", Body: "Set busy_timeout=5000 in config.go"},
		"MEM-DUP-2":  {ID: "MEM-DUP-2", Title: "Fix DB lock timeout", Body: "Set busy_timeout=5000 in config.go (duplicate note)"},
		"MEM-DIFF-3": {ID: "MEM-DIFF-3", Title: "Implement RLS in postgres", Body: "Row level security for tenant isolation"},
	}

	// Apply diverse reranking with limit = 2
	reranked, err := reranker.Rerank(ctx, candidates, records, 2)
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}

	if len(reranked) != 2 {
		t.Fatalf("expected top-2 diverse results, got %d", len(reranked))
	}
	// MEM-DUP-1 should be included (#1), MEM-DUP-2 suppressed, and MEM-DIFF-3 included (#2)
	if reranked[0].MemoryID != "MEM-DUP-1" || reranked[1].MemoryID != "MEM-DIFF-3" {
		t.Fatalf("expected diverse selection [MEM-DUP-1, MEM-DIFF-3], got: [%s, %s]", reranked[0].MemoryID, reranked[1].MemoryID)
	}

	// 2. Security invariant: Reranker output contains only IDs from the input candidate list
	for _, r := range reranked {
		if r.MemoryID != "MEM-DUP-1" && r.MemoryID != "MEM-DUP-2" && r.MemoryID != "MEM-DIFF-3" {
			t.Fatalf("unauthorized memory ID injected by reranker: %s", r.MemoryID)
		}
	}
}
