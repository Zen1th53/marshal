//go:build !governed

package execution

import (
	"errors"
	"testing"
	"time"
)

// A parent whose lane cannot be controlled reports the lane's error and
// returns: it never waits on the engine lock it already holds.
func TestControlRunLaneErrorReturns(t *testing.T) {
	e, parent, base := laneFixture(t)
	lane, err := e.OpenTaskLane(t.Context(), parent.RunID, "task-1", base)
	if err != nil {
		t.Fatal(err)
	}
	// A lane that no longer names this parent makes its control fail.
	store := e.store.(*FileRunStore)
	store.mem.mu.Lock()
	moved := store.mem.runs[lane.RunID]
	moved.ParentRunID = "another-parent"
	store.mem.runs[lane.RunID] = moved
	store.mem.mu.Unlock()
	current, err := e.GetRun(t.Context(), parent.RunID)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := e.ControlRun(t.Context(), parent.RunID, "pause", current.Version)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrRunConflict) {
			t.Fatalf("pausing a parent whose lane moved: %v, want the lane's conflict", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ControlRun deadlocked on a lane control error")
	}
}
