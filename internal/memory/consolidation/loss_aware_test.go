package consolidation_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/consolidation"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT149InformationLossAwareConsolidation(t *testing.T) {
	ctx := context.Background()
	c := consolidation.NewLossAwareConsolidator()

	sources := []model.MemoryRecordV2{
		{
			ID:    "MEM-1",
			Title: "SQLite WAL Mode Decision",
			Body:  "PRAGMA journal_mode=WAL; improves write concurrency.",
		},
		{
			ID:    "MEM-2",
			Title: "Network Share Exception",
			Body:  "CAUTION: WAL mode fails on network shares / NFS mounts due to shared memory lock failure.",
		},
	}

	// 1. Lossy summary that drops the critical NFS exception is REJECTED
	badSummary := "PRAGMA journal_mode=WAL is always recommended for all database deployments."
	_, err := c.Consolidate(ctx, sources, badSummary)
	if !errors.Is(err, consolidation.ErrInformationLossViolation) {
		t.Fatalf("expected ErrInformationLossViolation for summary omitting critical exception, got: %v", err)
	}

	// 2. High-fidelity summary that retains the critical exception is ACCEPTED
	goodSummary := "PRAGMA journal_mode=WAL improves write concurrency, but CAUTION: WAL mode fails on network shares/NFS."
	consolidated, err := c.Consolidate(ctx, sources, goodSummary)
	if err != nil {
		t.Fatalf("expected valid consolidation, got: %v", err)
	}
	if consolidated.SourceSetDigest == "" || len(consolidated.SourceIDs) != 2 {
		t.Fatalf("expected source set digest and source IDs recorded, got: %+v", consolidated)
	}
}
