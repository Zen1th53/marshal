package checkpoint_test

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/checkpoint"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT98CheckpointRestoreRevalidation(t *testing.T) {
	mgr := checkpoint.NewManager()
	ctx := context.Background()
	now := time.Now().UTC()

	// 1. Initial canonical memory records
	activeRec := model.MemoryRecordV2{
		ID:        "MEM-ACTIVE",
		ProjectID: "PROJ-1",
		Lifecycle: model.MemoryDurable,
	}

	supersededOld := model.MemoryRecordV2{
		ID:           "MEM-OLD",
		ProjectID:    "PROJ-1",
		Lifecycle:    model.MemorySuperseded,
		SupersededBy: []string{"MEM-NEW"},
	}

	tombstonedRec := model.MemoryRecordV2{
		ID:        "MEM-TOMB",
		ProjectID: "PROJ-1",
		Lifecycle: model.MemoryTombstoned,
	}

	memoryIndex := map[string]model.MemoryRecordV2{
		"MEM-ACTIVE": activeRec,
		"MEM-OLD":    supersededOld,
		"MEM-NEW":    {ID: "MEM-NEW", ProjectID: "PROJ-1", Lifecycle: model.MemoryDurable},
		"MEM-TOMB":   tombstonedRec,
	}

	// 2. Create checkpoint capturing session state referencing these memory IDs
	cp, err := mgr.CreateCheckpoint(ctx, checkpoint.CheckpointInput{
		ProjectID:         "PROJ-1",
		TaskID:            "TASK-1",
		SessionID:         "SESS-1",
		StepNumber:        42,
		AttachedMemoryIDs: []string{"MEM-ACTIVE", "MEM-OLD", "MEM-TOMB"},
		StateSnapshot:     map[string]any{"instruction": "resume compilation"},
		CreatedAt:         now,
	})
	if err != nil {
		t.Fatalf("CreateCheckpoint: %v", err)
	}

	// 3. Restore checkpoint with live memory state revalidation
	restored, err := mgr.RestoreCheckpoint(ctx, cp.CheckpointID, func(id string) (model.MemoryRecordV2, bool) {
		rec, ok := memoryIndex[id]
		return rec, ok
	})
	if err != nil {
		t.Fatalf("RestoreCheckpoint: %v", err)
	}

	// MEM-ACTIVE must remain attached
	if !contains(restored.ResolvedMemoryIDs, "MEM-ACTIVE") {
		t.Fatal("expected MEM-ACTIVE in restored memory IDs")
	}

	// MEM-OLD (superseded) must resolve to MEM-NEW
	if !contains(restored.ResolvedMemoryIDs, "MEM-NEW") {
		t.Fatal("expected superseded MEM-OLD to resolve to MEM-NEW")
	}
	if contains(restored.ResolvedMemoryIDs, "MEM-OLD") {
		t.Fatal("superseded MEM-OLD should not be directly attached")
	}

	// MEM-TOMB (tombstoned) must be excluded entirely
	if contains(restored.ResolvedMemoryIDs, "MEM-TOMB") {
		t.Fatal("tombstoned memory must not be resurrected by checkpoint restore")
	}
}

func contains(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}
