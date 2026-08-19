package versioning_test

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/versioning"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT99SnapshotDigestStabilityAndDiff(t *testing.T) {
	mgr := versioning.NewManager()
	ctx := context.Background()
	now := time.Now().UTC()

	rec1 := model.MemoryRecordV2{
		ID:            "MEM-1",
		ProjectID:     "PROJ-1",
		Revision:      1,
		ContentDigest: "digest-1111",
		ObservedAt:    now,
		CreatedAt:     now,
	}

	rec2 := model.MemoryRecordV2{
		ID:            "MEM-2",
		ProjectID:     "PROJ-1",
		Revision:      1,
		ContentDigest: "digest-2222",
		ObservedAt:    now,
		CreatedAt:     now,
	}

	// 1. Create Snapshot A (MEM-1, MEM-2)
	snapA, err := mgr.CreateSnapshot(ctx, "PROJ-1", "v1.0-baseline", []model.MemoryRecordV2{rec1, rec2})
	if err != nil {
		t.Fatalf("CreateSnapshot A: %v", err)
	}
	if snapA.ManifestDigest == "" {
		t.Fatal("expected non-empty ManifestDigest")
	}

	// Stability: Re-creating identical snapshot must produce exact same manifest digest
	snapACopy, err := mgr.CreateSnapshot(ctx, "PROJ-1", "v1.0-copy", []model.MemoryRecordV2{rec2, rec1}) // reversed order
	if err != nil {
		t.Fatal(err)
	}
	if snapA.ManifestDigest != snapACopy.ManifestDigest {
		t.Fatalf("manifest digest should be independent of input record order: %s != %s", snapA.ManifestDigest, snapACopy.ManifestDigest)
	}

	// 2. Create Snapshot B: MEM-1 modified (rev 2), MEM-2 removed, MEM-3 added
	rec1Mod := rec1
	rec1Mod.Revision = 2
	rec1Mod.ContentDigest = "digest-1111-updated"

	rec3 := model.MemoryRecordV2{
		ID:            "MEM-3",
		ProjectID:     "PROJ-1",
		Revision:      1,
		ContentDigest: "digest-3333",
		ObservedAt:    now,
		CreatedAt:     now,
	}

	snapB, err := mgr.CreateSnapshot(ctx, "PROJ-1", "v2.0-evolved", []model.MemoryRecordV2{rec1Mod, rec3})
	if err != nil {
		t.Fatalf("CreateSnapshot B: %v", err)
	}

	// 3. Diff Snapshots
	diff := mgr.DiffSnapshots(snapA, snapB)
	if len(diff.Added) != 1 || diff.Added[0] != "MEM-3" {
		t.Fatalf("expected Added=[MEM-3], got: %+v", diff.Added)
	}
	if len(diff.Removed) != 1 || diff.Removed[0] != "MEM-2" {
		t.Fatalf("expected Removed=[MEM-2], got: %+v", diff.Removed)
	}
	if len(diff.Modified) != 1 || diff.Modified[0] != "MEM-1" {
		t.Fatalf("expected Modified=[MEM-1], got: %+v", diff.Modified)
	}
}
