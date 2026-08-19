package fusion_test

import (
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/retrieval/fusion"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT110RRFFusionAndExplainableScoring(t *testing.T) {
	fuser := fusion.NewFuser(fusion.Config{
		K: 60,
	})
	now := time.Now().UTC()

	// Channel 1: Lexical rankings (raw scores 100.0, 50.0)
	lexicalRankings := []fusion.ChannelMatch{
		{MemoryID: "MEM-A", Rank: 1, RawScore: 100.0},
		{MemoryID: "MEM-B", Rank: 2, RawScore: 50.0},
	}

	// Channel 2: Dense Vector rankings (raw cosine scores 0.95, 0.90)
	vectorRankings := []fusion.ChannelMatch{
		{MemoryID: "MEM-B", Rank: 1, RawScore: 0.95}, // Ranked #1 in vector, #2 in lexical
		{MemoryID: "MEM-A", Rank: 2, RawScore: 0.90}, // Ranked #2 in vector, #1 in lexical
	}

	records := map[string]model.MemoryRecordV2{
		"MEM-A": {
			ID:         "MEM-A",
			Lifecycle:  model.MemoryDurable,
			Authority:  model.AuthorityOperator,
			ObservedAt: now,
		},
		"MEM-B": {
			ID:         "MEM-B",
			Lifecycle:  model.MemoryStale, // Stale record
			Authority:  model.AuthorityAgent,
			ObservedAt: now.Add(-1000 * time.Hour),
		},
	}

	// Fuse rankings
	results := fuser.Fuse([][]fusion.ChannelMatch{lexicalRankings, vectorRankings}, records, []string{"MEM-A"}, 10)

	if len(results) != 2 {
		t.Fatalf("expected 2 fused results, got %d", len(results))
	}

	// MEM-A (Durable + Operator + Exact Boost) must outrank MEM-B (Stale penalty)
	if results[0].MemoryID != "MEM-A" {
		t.Fatalf("expected MEM-A to rank #1 after fusion & penalties, got %s", results[0].MemoryID)
	}

	if results[0].Breakdown.ExactMatchBoost <= 0 {
		t.Fatalf("expected positive ExactMatchBoost on MEM-A, got: %f", results[0].Breakdown.ExactMatchBoost)
	}

	if results[1].Breakdown.LifecyclePenalty <= 0 {
		t.Fatalf("expected positive LifecyclePenalty on stale MEM-B, got: %f", results[1].Breakdown.LifecyclePenalty)
	}

	// Deterministic repeated ordering
	repeat := fuser.Fuse([][]fusion.ChannelMatch{lexicalRankings, vectorRankings}, records, []string{"MEM-A"}, 10)
	if repeat[0].MemoryID != results[0].MemoryID || repeat[1].MemoryID != results[1].MemoryID {
		t.Fatal("RRF fusion is not deterministic across runs")
	}
}
