//go:build !governed

package execution

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// Current selection is atomic and snapshots remain detached; a foreign parent
// is never supplied by a caller or inferred from an arbitrary map label.
func TestCurrentRequiredTaskFenceOwnsParentAndBlocksStoreWriter(t *testing.T) {
	engine, parent, child, _, cleanup := requiredSnapshotLaneFixture(t)
	defer cleanup()
	changed := parent
	changed.OriginalRequest += " owned sibling metadata"
	entered := make(chan struct{})
	release := make(chan struct{})
	fenced := make(chan error, 1)
	go func() {
		fenced <- engine.WithCurrentRequiredTaskFence(t.Context(), child.RunID, func(snapshots map[string]ExecutionRun) error {
			if len(snapshots) != 2 || snapshots[parent.RunID].Version != parent.Version || snapshots[child.RunID].ParentRunID != parent.RunID {
				return ErrRunConflict
			}
			copy := snapshots[parent.RunID]
			copy.OriginalRequest = "detached foreign text"
			snapshots[parent.RunID] = copy
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	written := make(chan error, 1)
	go func() { written <- engine.RunStore().UpdateRun(t.Context(), changed) }()
	select {
	case err := <-written:
		close(release)
		t.Fatal("writer crossed current owned task fence", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-fenced; err != nil {
		t.Fatal(err)
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	current, err := engine.GetRun(t.Context(), parent.RunID)
	if err != nil || current.OriginalRequest != changed.OriginalRequest || strings.Contains(current.OriginalRequest, "detached foreign") {
		t.Fatal("snapshot alias or lost canonical writer", err)
	}
}

func TestCurrentRequiredTaskFenceRefusesMissingForeignAndCancelled(t *testing.T) {
	engine, parent, child, _, cleanup := requiredSnapshotLaneFixture(t)
	defer cleanup()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	called := false
	if err := engine.WithCurrentRequiredTaskFence(ctx, child.RunID, func(map[string]ExecutionRun) error { called = true; return nil }); !errors.Is(err, context.Canceled) || called {
		t.Fatal("cancelled current task fence invoked effect", err)
	}
	if err := engine.WithCurrentRequiredTaskFence(t.Context(), "foreign", func(map[string]ExecutionRun) error { called = true; return nil }); err == nil || called {
		t.Fatal("foreign current task fence invoked effect")
	}
	if err := engine.WithCurrentRequiredTaskFence(t.Context(), child.RunID, nil); err == nil {
		t.Fatal("nil effect accepted")
	}
	forged := child
	forged.ParentRunID = "foreign-parent"
	if err := engine.RunStore().UpdateRun(t.Context(), forged); err != nil {
		t.Fatal(err)
	}
	if err := engine.WithCurrentRequiredTaskFence(t.Context(), child.RunID, func(map[string]ExecutionRun) error { called = true; return nil }); err == nil || called {
		t.Fatal("missing current actual parent accepted")
	}
	latest, err := engine.GetRun(t.Context(), child.RunID)
	if err != nil {
		t.Fatal(err)
	}
	latest.ParentRunID = parent.RunID
	if err := engine.RunStore().UpdateRun(t.Context(), latest); err != nil {
		t.Fatal(err)
	}
}

type unownedCurrentFenceStore struct{ RunStore }

func TestCurrentRequiredTaskFenceRejectsUnavailableStoresBeforeEffect(t *testing.T) {
	called := false
	effect := func(map[string]ExecutionRun) error { called = true; return nil }
	var memory *MemoryRunStore
	var file *FileRunStore
	for _, call := range []func() error{
		func() error { return memory.withCurrentRequiredTaskFence(t.Context(), "run", effect) },
		func() error { return file.withCurrentRequiredTaskFence(t.Context(), "run", effect) },
		func() error { return (&FileRunStore{}).withCurrentRequiredTaskFence(t.Context(), "run", effect) },
		func() error {
			return (&Engine{store: &unownedCurrentFenceStore{}}).WithCurrentRequiredTaskFence(t.Context(), "run", effect)
		},
	} {
		if err := call(); !errors.Is(err, ErrRunBlocked) || called {
			t.Fatal("unowned or unavailable store acquired fence", err)
		}
	}
}
