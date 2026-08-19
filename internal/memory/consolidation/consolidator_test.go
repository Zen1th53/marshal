package consolidation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/consolidation"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT116MemoryConsolidationAndSourceTraceability(t *testing.T) {
	c := consolidation.NewConsolidator()
	ctx := context.Background()
	now := time.Now().UTC()

	ep1 := model.MemoryRecordV2{
		ID:         "MEM-EP-1",
		ProjectID:  "PROJ-1",
		Kind:       model.MemoryKindEpisodic,
		Title:      "Run DB Migration Step 1",
		Body:       "Executed migration v68 for memory_records_v2 table.",
		ObservedAt: now,
		CreatedAt:  now,
	}

	ep2 := model.MemoryRecordV2{
		ID:         "MEM-EP-2",
		ProjectID:  "PROJ-1",
		Kind:       model.MemoryKindEpisodic,
		Title:      "Run DB Migration Step 2",
		Body:       "Executed migration v69 for memory_outbox table.",
		ObservedAt: now,
		CreatedAt:  now,
	}

	// 1. Consolidate episodes into a coherent summary
	summaryRec, err := c.ConsolidateEpisodes(ctx, "PROJ-1", "Database Schema Upgrades", []model.MemoryRecordV2{ep1, ep2})
	if err != nil {
		t.Fatalf("ConsolidateEpisodes: %v", err)
	}

	if summaryRec.Kind != model.MemoryKindSemantic {
		t.Fatalf("expected semantic kind for consolidated summary, got: %s", summaryRec.Kind)
	}
	if len(summaryRec.EvidenceIDs) != 2 {
		t.Fatalf("expected 2 source evidence IDs preserved in summary, got %d", len(summaryRec.EvidenceIDs))
	}

	// 2. Stable identity: Re-consolidating same source set produces same deterministic ID
	summaryRec2, _ := c.ConsolidateEpisodes(ctx, "PROJ-1", "Database Schema Upgrades", []model.MemoryRecordV2{ep2, ep1})
	if summaryRec.ID != summaryRec2.ID {
		t.Fatalf("expected deterministic ID for consolidated record: %s != %s", summaryRec.ID, summaryRec2.ID)
	}

	// 3. Contradictory episodes must be rejected from false consolidation collapse
	contradictoryEp := model.MemoryRecordV2{
		ID:         "MEM-EP-CONTRADICT",
		ProjectID:  "PROJ-1",
		Kind:       model.MemoryKindEpisodic,
		Lifecycle:  model.MemoryConflicted,
		Title:      "Failed Migration",
		Body:       "Migration v68 failed due to SQLite lock contention.",
		ObservedAt: now,
		CreatedAt:  now,
	}

	_, err = c.ConsolidateEpisodes(ctx, "PROJ-1", "Database Schema Upgrades", []model.MemoryRecordV2{ep1, contradictoryEp})
	if !errors.Is(err, consolidation.ErrContradictorySources) {
		t.Fatalf("expected ErrContradictorySources when consolidating conflicting episodes, got: %v", err)
	}
}
