package portable_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/mutation"
	"github.com/Zen1th53/marshal/internal/memory/portable"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT157PortableMemoryExportImport(t *testing.T) {
	ctx := context.Background()
	mgr := portable.NewManager()
	now := time.Now().UTC()

	records := []model.MemoryRecordV2{
		{
			ID:         "MEM-EXPORT-01",
			Title:      "Canonical Architecture Fact",
			Body:       "Use SQLite WAL mode",
			Lifecycle:  model.MemoryDurable,
			Authority:  model.AuthorityOperator,
			ObservedAt: now,
		},
		{
			ID:         "MEM-TOMBSTONE-01",
			Title:      "Obsolete Config",
			Body:       "Deleted config item",
			Lifecycle:  model.MemoryTombstoned,
			ObservedAt: now,
		},
	}

	envelopes := []mutation.MutationEnvelope{
		{
			MutationPayload: mutation.MutationPayload{
				MemoryID:      "MEM-EXPORT-01",
				NewRevision:   1,
				ContentDigest: "digest-1",
			},
			Epoch: 1,
		},
	}

	// 1. Export manifest
	manifest, err := mgr.Export(ctx, records, envelopes)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if manifest.Version != portable.CurrentManifestVersion {
		t.Fatalf("expected version %s, got: %s", portable.CurrentManifestVersion, manifest.Version)
	}

	// 2. Import roundtrip
	importedRecords, err := mgr.Import(ctx, manifest, portable.ImportOptions{DryRun: false})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(importedRecords) != 2 {
		t.Fatalf("expected 2 imported records, got %d", len(importedRecords))
	}

	// 3. Tombstone preservation
	if importedRecords[1].Lifecycle != model.MemoryTombstoned {
		t.Fatalf("expected tombstone lifecycle preserved, got: %s", importedRecords[1].Lifecycle)
	}

	// 4. Unknown future version rejected
	badManifest := manifest
	badManifest.Version = "99.0.0"
	_, err = mgr.Import(ctx, badManifest, portable.ImportOptions{DryRun: false})
	if !errors.Is(err, portable.ErrUnsupportedSchemaVersion) {
		t.Fatalf("expected ErrUnsupportedSchemaVersion for future version 99.0.0, got: %v", err)
	}
}
