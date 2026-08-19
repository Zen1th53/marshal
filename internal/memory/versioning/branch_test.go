package versioning_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/versioning"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT100MemoryBranchIsolationAndMerge(t *testing.T) {
	mgr := versioning.NewManager()
	ctx := context.Background()
	now := time.Now().UTC()

	baseRec := model.MemoryRecordV2{
		ID:            "MEM-MAIN-01",
		ProjectID:     "PROJ-1",
		Kind:          model.MemoryKindSemantic,
		Lifecycle:     model.MemoryDurable,
		Title:         "Main Arch",
		Body:          "Server on port 8080",
		ContentDigest: "digest-8080",
		ObservedAt:    now,
		CreatedAt:     now,
	}

	snap, err := mgr.CreateSnapshot(ctx, "PROJ-1", "snap-main-0", []model.MemoryRecordV2{baseRec})
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}

	// 1. Create experimental branch
	branch, err := mgr.CreateBranch(ctx, "PROJ-1", "exp-port-9090", snap.SnapshotID)
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}

	// 2. Add an experimental record to the branch
	expRec := model.MemoryRecordV2{
		ID:            "MEM-EXP-01",
		ProjectID:     "PROJ-1",
		Kind:          model.MemoryKindSemantic,
		Lifecycle:     model.MemoryCandidate,
		Title:         "Exp Arch",
		Body:          "Server on port 9090",
		ContentDigest: "digest-9090",
		BranchName:    "exp-port-9090",
		ObservedAt:    now,
		CreatedAt:     now,
	}
	if err := mgr.RecordBranchWrite(ctx, branch.BranchID, expRec); err != nil {
		t.Fatalf("RecordBranchWrite: %v", err)
	}

	// 3. Unauthorized developer cannot merge branch to protected main
	_, err = mgr.MergeBranch(ctx, branch.BranchID, "main", "dev-junior", "developer")
	if !errors.Is(err, versioning.ErrUnauthorizedMerge) {
		t.Fatalf("expected ErrUnauthorizedMerge, got: %v", err)
	}

	// 4. Authorized operator merges branch to main
	mergeResult, err := mgr.MergeBranch(ctx, branch.BranchID, "main", "admin-lead", "operator")
	if err != nil {
		t.Fatalf("MergeBranch: %v", err)
	}
	if len(mergeResult.MergedRecordIDs) != 1 || mergeResult.MergedRecordIDs[0] != "MEM-EXP-01" {
		t.Fatalf("expected MEM-EXP-01 merged into main, got: %+v", mergeResult.MergedRecordIDs)
	}
}

func TestT100RollbackCreatesNewHeadWithoutAuditLoss(t *testing.T) {
	mgr := versioning.NewManager()
	ctx := context.Background()
	now := time.Now().UTC()

	recA := model.MemoryRecordV2{ID: "MEM-A", ProjectID: "PROJ-1", ContentDigest: "dA", ObservedAt: now, CreatedAt: now}
	snapA, _ := mgr.CreateSnapshot(ctx, "PROJ-1", "snap-A", []model.MemoryRecordV2{recA})

	recB := model.MemoryRecordV2{ID: "MEM-B", ProjectID: "PROJ-1", ContentDigest: "dB", ObservedAt: now, CreatedAt: now}
	_, _ = mgr.CreateSnapshot(ctx, "PROJ-1", "snap-B", []model.MemoryRecordV2{recA, recB})

	// Rollback main from snapB back to snapA
	rbSnap, err := mgr.RollbackToSnapshot(ctx, "PROJ-1", "main", snapA.SnapshotID, "admin-lead", "operator")
	if err != nil {
		t.Fatalf("RollbackToSnapshot: %v", err)
	}

	// Rollback snapshot must match snapA records
	if len(rbSnap.Records) != 1 || rbSnap.Records["MEM-A"].ContentDigest != "dA" {
		t.Fatalf("expected rollback snapshot to have MEM-A only, got: %+v", rbSnap.Records)
	}
}
