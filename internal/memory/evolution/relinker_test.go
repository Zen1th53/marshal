package evolution_test

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/evolution"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT140AgenticMemoryEvolutionAndSafeRelinking(t *testing.T) {
	ctx := context.Background()
	relinker := evolution.NewSafeRelinker()
	now := time.Now().UTC()

	// 1. Old historical record
	oldRec := model.MemoryRecordV2{
		ID:         "MEM-OLD-01",
		ProjectID:  "PROJ-1",
		Title:      "SQLite Multi-reader Issue",
		Body:       "Observed database locked errors under concurrent agent runs",
		Lifecycle:  model.MemoryDurable,
		Authority:  model.AuthorityOperator,
		ObservedAt: now,
	}
	oldRec.ContentDigest = oldRec.CanonicalDigest()
	initialDigest := oldRec.ContentDigest

	// 2. New decision record that resolves the older issue
	newDecision := model.MemoryRecordV2{
		ID:         "MEM-NEW-DECISION",
		ProjectID:  "PROJ-1",
		Title:      "Enable SQLite WAL Mode",
		Body:       "PRAGMA journal_mode=WAL; enables concurrent readers while writing",
		Lifecycle:  model.MemoryDurable,
		Authority:  model.AuthorityOperator,
		ObservedAt: now,
	}
	newDecision.ContentDigest = newDecision.CanonicalDigest()

	// 3. Perform safe re-linking
	links, err := relinker.EvolveLinks(ctx, newDecision, []model.MemoryRecordV2{oldRec})
	if err != nil {
		t.Fatalf("EvolveLinks: %v", err)
	}

	if len(links) != 1 {
		t.Fatalf("expected 1 derived link, got: %d", len(links))
	}
	if links[0].FromID != newDecision.ID || links[0].ToID != oldRec.ID || links[0].Relation != "resolves" {
		t.Fatalf("unexpected link shape: %+v", links[0])
	}

	// 4. Invariant: Historical payload and digest MUST NOT be modified
	if oldRec.ContentDigest != initialDigest {
		t.Fatal("CRITICAL: historical memory record digest was mutated during re-linking!")
	}
}
