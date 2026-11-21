//go:build !governed

package execution

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// finishLaneInStore records lane as a completed attempt, as a worker that
// handed in would leave it, without running a worker: the attempt's own
// execution is covered elsewhere and needs a Linux worktree.
func finishLaneInStore(t *testing.T, e *Engine, id string) {
	t.Helper()
	lane := mustRun(t, e, id)
	task := lane.Tasks[lane.LaneTaskID]
	now := time.Now().UTC()
	task.State = TaskCompletedPendingVerify
	task.Attempts = 1
	task.StartedAt = &now
	task.ResultCommit = lane.BaseCommit
	lane.Tasks[lane.LaneTaskID] = task
	lane.State = RunDonePendingVerification
	if err := e.store.UpdateRun(t.Context(), lane); err != nil {
		t.Fatal(err)
	}
}

func advanceHead(t *testing.T, root string) string {
	t.Helper()
	for _, args := range [][]string{{"commit", "--allow-empty", "-m", "dependency merged"}, {"rev-parse", "HEAD"}} {
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		if args[0] == "rev-parse" {
			return strings.TrimSpace(string(out))
		}
	}
	return ""
}

// A task built on one worker, then reassigned to another (/marshal reassign,
// or a Marshal-proposed reassignment the operator accepted), opens its next
// attempt on the new worker. A reassigned task with dependencies starts from
// the integration head, which has moved since its first attempts (production:
// rv06, "durable task lane binding differs"). The switch retires the earlier
// attempts' binding, base included; without a switch, or for a binding the
// switch never retired, the conflict check still refuses.
func TestExtremeReassignedTaskLaneOpensOnNewWorkerFromNewerBase(t *testing.T) {
	e, parent, base := laneFixture(t)
	registerWritingHarness(e)
	e.RegisterHarness(NewMockHarness("mock2", func(_ context.Context, task TaskExecution, _ ConstraintPackage, _ string) (TaskResult, error) {
		return TaskResult{TaskID: task.TaskID, Success: true}, nil
	}))
	ctx := t.Context()
	// Worker A builds the task twice (a first attempt and one rework), both
	// from the base it was first dispatched on.
	first, err := e.OpenTaskLane(ctx, parent.RunID, "task-1", base)
	if err != nil {
		t.Fatal(err)
	}
	finishLaneInStore(t, e, first.RunID)
	second, err := e.OpenTaskLaneRework(ctx, parent.RunID, "task-1", base, 1)
	if err != nil {
		t.Fatal(err)
	}
	finishLaneInStore(t, e, second.RunID)

	// Other tasks merge meanwhile: the integration head moves.
	newer := advanceHead(t, parent.Provenance.RepoRoot)

	// Without a reassignment a rework keeps its original base.
	if _, err = e.OpenTaskLaneRework(ctx, parent.RunID, "task-1", newer, 2); !errors.Is(err, ErrRunConflict) || !strings.Contains(err.Error(), "durable task lane binding differs") {
		t.Fatalf("an unswitched rework opened from another base: %v", err)
	}

	current := mustRun(t, e, parent.RunID)
	from := current.Tasks["task-1"].AssignedHarness
	allow := func(ExecutionRun, TaskExecution) error { return nil }
	if _, err = e.RebindTaskLaneProviderSwitch(ctx, parent.RunID, current.Version, "task-1", from, "mock2", "model-2", "receipt-reassign", allow); err != nil {
		t.Fatal(err)
	}

	// A current attempt whose binding the switch did not retire is foreign:
	// still refused.
	retained := mustRun(t, e, second.RunID)
	genuine := retained.ParentBinding
	retained.ParentBinding = strings.Repeat("f", len(genuine))
	if err = e.store.UpdateRun(ctx, retained); err != nil {
		t.Fatal(err)
	}
	if _, err = e.OpenTaskLaneRework(ctx, parent.RunID, "task-1", newer, 2); !errors.Is(err, ErrRunConflict) || !strings.Contains(err.Error(), "durable task lane binding differs") {
		t.Fatalf("a foreign lane binding was admitted: %v", err)
	}
	retained = mustRun(t, e, second.RunID)
	retained.ParentBinding = genuine
	if err = e.store.UpdateRun(ctx, retained); err != nil {
		t.Fatal(err)
	}

	// Worker B's attempt opens from the newer base.
	third, err := e.OpenTaskLaneRework(ctx, parent.RunID, "task-1", newer, 2)
	if err != nil {
		t.Fatalf("the reassigned task's next attempt was refused: %v", err)
	}
	latest := mustRun(t, e, parent.RunID)
	task := third.Tasks["task-1"]
	if third.BaseCommit != newer || task.BaseCommit != newer || task.AssignedHarness != "mock2" || third.ParentBinding != taskLaneBinding(latest, latest.Tasks["task-1"], newer) || latest.TaskLanes["task-1"] != third.RunID {
		t.Fatalf("reassigned attempt binding: base %s, harness %s", third.BaseCommit, task.AssignedHarness)
	}
	// The earlier attempts keep their results.
	for _, id := range []string{first.RunID, second.RunID} {
		if kept := mustRun(t, e, id); kept.Tasks["task-1"].ResultCommit == "" || kept.BaseCommit != base {
			t.Fatalf("earlier attempt %s was not retained", id)
		}
	}
	// Reopening the same attempt is idempotent; a later rework of the
	// switched attempt keeps the switched base again.
	if again, err := e.OpenTaskLaneRework(ctx, parent.RunID, "task-1", newer, 2); err != nil || again.RunID != third.RunID {
		t.Fatalf("reopening the reassigned attempt: %v", err)
	}
	finishLaneInStore(t, e, third.RunID)
	if _, err = e.OpenTaskLaneRework(ctx, parent.RunID, "task-1", base, 3); !errors.Is(err, ErrRunConflict) {
		t.Fatalf("a rework of the switched attempt opened from another base: %v", err)
	}
	if _, err = e.OpenTaskLaneRework(ctx, parent.RunID, "task-1", newer, 3); err != nil {
		t.Fatalf("a rework of the switched attempt was refused: %v", err)
	}
}
