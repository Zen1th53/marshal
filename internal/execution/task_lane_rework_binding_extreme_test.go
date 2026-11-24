//go:build !governed

package execution

import (
	"errors"
	"strings"
	"testing"
)

// A rework lane keeps the approved task binding and base of the first
// attempt, retains that attempt's result, and refuses a different base.
func TestLaneReworkKeepsApprovedBinding(t *testing.T) {
	e, parent, base := laneFixture(t)
	registerWritingHarness(e)
	first, err := e.OpenTaskLane(t.Context(), parent.RunID, "task-1", base)
	if err != nil {
		t.Fatal(err)
	}
	done := executeLane(t, e, first, base)
	prior := done.Tasks["task-1"].ResultCommit
	if _, err := e.OpenTaskLaneRework(t.Context(), parent.RunID, "task-1", strings.Repeat("1", len(base)), 1); !errors.Is(err, ErrRunConflict) {
		t.Fatalf("rework on another base = %v, want a binding conflict", err)
	}
	second, err := e.OpenTaskLaneRework(t.Context(), parent.RunID, "task-1", base, 1)
	if err != nil {
		t.Fatal(err)
	}
	if second.ParentBinding != first.ParentBinding || second.BaseCommit != base || second.ParentRunID != parent.RunID {
		t.Fatalf("rework binding = %s base %s, want %s base %s", second.ParentBinding, second.BaseCommit, first.ParentBinding, base)
	}
	want, got := first.Tasks["task-1"], second.Tasks["task-1"]
	if got.TaskID != want.TaskID || strings.Join(got.TargetFiles, ",") != strings.Join(want.TargetFiles, ",") || got.BaseCommit != base {
		t.Fatalf("rework task definition changed: %+v", got)
	}
	retained := mustRun(t, e, first.RunID)
	if retained.Tasks["task-1"].ResultCommit != prior || retained.ParentBinding != first.ParentBinding {
		t.Fatal("prior attempt result or binding was not retained")
	}
}
