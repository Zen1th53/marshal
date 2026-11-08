package checkpoint_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/checkpoint"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestCheckpointMemoryRevalidationBoundsCyclesAndForeignReferences(t *testing.T) {
	for _, kind := range []string{"cycle", "foreign", "missing-successor", "tombstone-successor", "branch"} {
		t.Run(kind, func(t *testing.T) {
			m := checkpoint.NewManager()
			cp, err := m.CreateCheckpoint(context.Background(), checkpoint.CheckpointInput{ProjectID: "project", AttachedMemoryIDs: []string{"old"}})
			if err != nil {
				t.Fatal(err)
			}
			index := map[string]model.MemoryRecordV2{"old": {ID: "old", ProjectID: "project", Lifecycle: model.MemorySuperseded, SupersededBy: []string{"new"}}, "new": {ID: "new", ProjectID: "project", Lifecycle: model.MemoryDurable}}
			switch kind {
			case "cycle":
				index["new"] = model.MemoryRecordV2{ID: "new", ProjectID: "project", Lifecycle: model.MemorySuperseded, SupersededBy: []string{"old"}}
			case "foreign":
				r := index["new"]
				r.ProjectID = "foreign"
				index["new"] = r
			case "missing-successor":
				delete(index, "new")
			case "tombstone-successor":
				r := index["new"]
				r.Lifecycle = model.MemoryTombstoned
				index["new"] = r
			case "branch":
				r := index["old"]
				r.SupersededBy = []string{"new", "other"}
				index["old"] = r
			}
			got, err := m.RestoreCheckpoint(context.Background(), cp.CheckpointID, func(id string) (model.MemoryRecordV2, bool) { r, ok := index[id]; return r, ok })
			if kind == "cycle" || kind == "branch" {
				if !errors.Is(err, model.ErrConflict) {
					t.Fatalf("unbounded/ambiguous chain: %v", err)
				}
			} else if err != nil || len(got.ResolvedMemoryIDs) != 0 {
				t.Fatalf("inapplicable reference retained: %+v %v", got, err)
			}
		})
	}
}

func TestCheckpointSnapshotsAreDetachedAndRestoreHonorsCancellation(t *testing.T) {
	m := checkpoint.NewManager()
	input := checkpoint.CheckpointInput{ProjectID: "project", AttachedMemoryIDs: []string{"memory"}, StateSnapshot: map[string]any{"nested": map[string]any{"value": "original"}}}
	cp, err := m.CreateCheckpoint(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	input.AttachedMemoryIDs[0] = "changed"
	input.StateSnapshot["nested"].(map[string]any)["value"] = "changed"
	cp.StateSnapshot["nested"].(map[string]any)["value"] = "also changed"
	got, err := m.RestoreCheckpoint(context.Background(), cp.CheckpointID, func(id string) (model.MemoryRecordV2, bool) {
		return model.MemoryRecordV2{ID: id, ProjectID: "project", Lifecycle: model.MemoryDurable}, true
	})
	if err != nil || got.Checkpoint.AttachedMemoryIDs[0] != "memory" || got.Checkpoint.StateSnapshot["nested"].(map[string]any)["value"] != "original" {
		t.Fatalf("mutable snapshot: %+v %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = m.RestoreCheckpoint(ctx, cp.CheckpointID, func(string) (model.MemoryRecordV2, bool) {
		t.Fatal("resolver called after cancellation")
		return model.MemoryRecordV2{}, false
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
