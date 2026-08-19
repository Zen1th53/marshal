package store

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestT89StoreBitemporalPointInTimeQueries(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	projID := "PROJ-T89"
	if err := st.InitProject(ctx, model.Project{
		ID: projID, Repository: "repo", DefaultBranch: "main", PackVersion: "1.0.0",
	}); err != nil {
		t.Fatal(err)
	}

	tJan := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tFeb := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	tMar := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	tApr := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	// Historical fact valid in Jan-Feb, ingested in Feb
	rec1 := model.MemoryRecordV2{
		ID:         "MEM-HIST-01",
		ProjectID:  projID,
		Kind:       model.MemoryKindSemantic,
		Lifecycle:  model.MemoryDurable,
		Authority:  model.AuthorityVerified,
		Title:      "Initial Architecture",
		Body:       "Monolith design in Go",
		Scope:      string(model.ScopeProject),
		ScopeID:    projID,
		ObservedAt: tJan,
		IngestedAt: tFeb,
		ValidFrom:  tJan,
		ValidTo:    &tMar, // valid until March
		CreatedAt:  tFeb,
		UpdatedAt:  tFeb,
		Source:     model.MemorySource{Kind: "runtime", Reference: "task-1"},
	}
	if err := st.WriteMemoryV2(ctx, rec1); err != nil {
		t.Fatalf("WriteMemoryV2 rec1: %v", err)
	}

	// 1. Query as of mid-Jan (valid) known in March -> returns rec1
	tMidJan := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	res, err := st.ListMemoryV2(ctx, MemoryQueryFilter{
		ProjectID: projID,
		ValidAsOf: tMidJan,
		KnownAt:   tMar,
	})
	if err != nil {
		t.Fatalf("ListMemoryV2: %v", err)
	}
	if len(res) != 1 || res[0].ID != "MEM-HIST-01" {
		t.Fatalf("expected 1 record (MEM-HIST-01), got: %+v", res)
	}

	// 2. Query as of mid-Jan known at Jan 2 (before ingestion at Feb 1) -> returns 0
	tJan2 := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	resBeforeIngest, err := st.ListMemoryV2(ctx, MemoryQueryFilter{
		ProjectID: projID,
		ValidAsOf: tMidJan,
		KnownAt:   tJan2,
	})
	if err != nil {
		t.Fatalf("ListMemoryV2 before ingest: %v", err)
	}
	if len(resBeforeIngest) != 0 {
		t.Fatalf("expected 0 records known at Jan 2, got %d", len(resBeforeIngest))
	}

	// 3. Query as of April (after expiration at March 1) -> returns 0
	resAfterExp, err := st.ListMemoryV2(ctx, MemoryQueryFilter{
		ProjectID: projID,
		ValidAsOf: tApr,
	})
	if err != nil {
		t.Fatalf("ListMemoryV2 after exp: %v", err)
	}
	if len(resAfterExp) != 0 {
		t.Fatalf("expected 0 records valid in April, got %d", len(resAfterExp))
	}
}
