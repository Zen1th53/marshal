package finalize_test

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/retrieval/finalize"
	"github.com/Zen1th53/marshal/internal/memory/retrieval/fusion"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT112TemporalConflictAndSupersessionFinalizer(t *testing.T) {
	finalizer := finalize.NewFinalizer()
	ctx := context.Background()

	tOld := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	tNew := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := time.Now().UTC()

	// 1. Setup records:
	// - MEM-OLD (superseded by MEM-NEW at tNew)
	// - MEM-NEW (current durable truth)
	// - MEM-CONF (conflicted record)
	// - MEM-TOMB (tombstoned record)
	records := map[string]model.MemoryRecordV2{
		"MEM-OLD": {
			ID:           "MEM-OLD",
			ProjectID:    "PROJ-1",
			Lifecycle:    model.MemorySuperseded,
			ScopeID:      "scope-1",
			ValidFrom:    tOld,
			ValidTo:      &tNew,
			SupersededBy: []string{"MEM-NEW"},
		},
		"MEM-NEW": {
			ID:        "MEM-NEW",
			ProjectID: "PROJ-1",
			Lifecycle: model.MemoryDurable,
			ScopeID:   "scope-1",
			ValidFrom: tNew,
		},
		"MEM-CONF": {
			ID:          "MEM-CONF",
			ProjectID:   "PROJ-1",
			Lifecycle:   model.MemoryConflicted,
			ScopeID:     "scope-1",
			ConflictIDs: []string{"MEM-CONF-PEER"},
			ValidFrom:   tOld,
		},
		"MEM-TOMB": {
			ID:        "MEM-TOMB",
			ProjectID: "PROJ-1",
			Lifecycle: model.MemoryTombstoned,
			ScopeID:   "scope-1",
		},
	}

	candidates := []fusion.FusedResult{
		{MemoryID: "MEM-TOMB", RankScore: 0.99},
		{MemoryID: "MEM-OLD", RankScore: 0.90},
		{MemoryID: "MEM-NEW", RankScore: 0.85},
		{MemoryID: "MEM-CONF", RankScore: 0.80},
	}

	// 2. Current query (asOf now):
	// - MEM-TOMB must be completely filtered out
	// - MEM-OLD (superseded) must be suppressed in favor of MEM-NEW
	// - MEM-CONF must be flagged with IsConflicted=true
	currOut, err := finalizer.Finalize(ctx, candidates, records, finalize.Params{
		AsOf:            now,
		AllowedScopeIDs: []string{"scope-1"},
	})
	if err != nil {
		t.Fatalf("Finalize current: %v", err)
	}

	for _, item := range currOut {
		if item.MemoryID == "MEM-TOMB" {
			t.Fatal("tombstoned memory was not filtered out")
		}
		if item.MemoryID == "MEM-OLD" {
			t.Fatal("superseded old memory should be suppressed in current query when successor is valid")
		}
		if item.MemoryID == "MEM-CONF" && !item.IsConflicted {
			t.Fatal("conflicted memory must be explicitly flagged with IsConflicted")
		}
	}

	// 3. Historical query (asOf tOld):
	// - MEM-OLD must be returned with IsHistorical=true
	// - MEM-NEW (not yet valid at tOld) must not be returned as active
	histOut, err := finalizer.Finalize(ctx, candidates, records, finalize.Params{
		AsOf:            tOld,
		AllowedScopeIDs: []string{"scope-1"},
	})
	if err != nil {
		t.Fatalf("Finalize historical: %v", err)
	}

	foundOld := false
	for _, item := range histOut {
		if item.MemoryID == "MEM-OLD" {
			foundOld = true
			if !item.IsHistorical {
				t.Fatal("historical record must have IsHistorical=true")
			}
		}
	}
	if !foundOld {
		t.Fatal("expected MEM-OLD to be returned for historical query at tOld")
	}
}
