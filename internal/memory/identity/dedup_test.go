package identity_test

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/identity"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT87ExactDuplicateNormalizationAndMerge(t *testing.T) {
	mgr := identity.NewManager()
	ctx := context.Background()

	now := time.Now().UTC()

	rec1 := model.MemoryRecordV2{
		ID:          "MEM-DEDUP-01",
		ProjectID:   "PROJ-1",
		Kind:        model.MemoryKindSemantic,
		Lifecycle:   model.MemoryCandidate,
		Title:       "Port Configuration",
		Body:        "Server listens on port 8080 by default.",
		Scope:       string(model.ScopeProject),
		ScopeID:     "PROJ-1",
		ObservedAt:  now,
		ValidFrom:   now,
		EvidenceIDs: []string{"EVID-1"},
		Source:      model.MemorySource{Kind: "runtime", Reference: "run-1", AgentID: "agent-1"},
	}

	// rec2 has identical content but with extra whitespace and contributed by agent-2 with evidence EVID-2
	rec2 := model.MemoryRecordV2{
		ID:          "MEM-DEDUP-02",
		ProjectID:   "PROJ-1",
		Kind:        model.MemoryKindSemantic,
		Lifecycle:   model.MemoryCandidate,
		Title:       "Port Configuration",
		Body:        "  Server   listens on   port 8080 by default.  \n",
		Scope:       string(model.ScopeProject),
		ScopeID:     "PROJ-1",
		ObservedAt:  now,
		ValidFrom:   now,
		EvidenceIDs: []string{"EVID-2"},
		Source:      model.MemorySource{Kind: "runtime", Reference: "run-2", AgentID: "agent-2"},
	}

	// 1. Normalized body must match
	norm1 := mgr.NormalizeText(rec1.Body)
	norm2 := mgr.NormalizeText(rec2.Body)
	if norm1 != norm2 {
		t.Fatalf("normalized text mismatch: %q != %q", norm1, norm2)
	}

	// 2. Safe provenance merge preserves both evidence IDs
	merged, isDuplicate := mgr.MergeDuplicates(ctx, rec1, rec2)
	if !isDuplicate {
		t.Fatal("expected isDuplicate=true for normalized identical content")
	}
	if len(merged.EvidenceIDs) != 2 {
		t.Fatalf("expected 2 merged evidence IDs, got: %+v", merged.EvidenceIDs)
	}
	if merged.ID != rec1.ID {
		t.Fatalf("expected merged record to preserve canonical ID %s, got %s", rec1.ID, merged.ID)
	}
}

func TestT87ContradictoryValuesDoNotMerge(t *testing.T) {
	mgr := identity.NewManager()
	ctx := context.Background()

	now := time.Now().UTC()

	rec1 := model.MemoryRecordV2{
		ID:         "MEM-CONF-01",
		ProjectID:  "PROJ-1",
		Kind:       model.MemoryKindSemantic,
		Title:      "Port",
		Body:       "Server listens on port 8080",
		Scope:      string(model.ScopeProject),
		ScopeID:    "PROJ-1",
		ObservedAt: now,
		ValidFrom:  now,
	}

	rec2 := model.MemoryRecordV2{
		ID:         "MEM-CONF-02",
		ProjectID:  "PROJ-1",
		Kind:       model.MemoryKindSemantic,
		Title:      "Port",
		Body:       "Server listens on port 9090", // Contradictory value
		Scope:      string(model.ScopeProject),
		ScopeID:    "PROJ-1",
		ObservedAt: now,
		ValidFrom:  now,
	}

	_, isDuplicate := mgr.MergeDuplicates(ctx, rec1, rec2)
	if isDuplicate {
		t.Fatal("contradictory facts must not be classified as duplicates")
	}
}
