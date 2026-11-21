//go:build !governed

package execution

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/plan"
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

// failLaneInStore records lane as an attempt its provider failed, as a
// rate-limited worker would leave it, without running a worker.
func failLaneInStore(t *testing.T, e *Engine, id string) {
	t.Helper()
	lane := mustRun(t, e, id)
	task := lane.Tasks[lane.LaneTaskID]
	now := time.Now().UTC()
	task.State = TaskFailed
	task.Attempts = 1
	task.StartedAt = &now
	task.LastFailureReason = "provider rate limit exceeded"
	lane.Tasks[lane.LaneTaskID] = task
	lane.State = RunFailed
	if err := e.store.UpdateRun(t.Context(), lane); err != nil {
		t.Fatal(err)
	}
}

// Automatic provider failover follows the same rule as a reassignment: the
// failed attempt's binding (its base included) retires with the failover, so
// the replacement attempt opens on the fallback provider from the newer
// integration head. A failed lane whose binding is foreign is still refused,
// and without a failover a rework at another base is still refused.
func TestExtremeProviderFailoverLaneOpensOnFallbackFromNewerBase(t *testing.T) {
	e, parent, base := laneFixture(t)
	registerWritingHarness(e)
	e.RegisterHarness(NewMockHarness("mock2", func(_ context.Context, task TaskExecution, _ ConstraintPackage, _ string) (TaskResult, error) {
		return TaskResult{TaskID: task.TaskID, Success: true}, nil
	}))
	ctx := WithVerifiedFallbackModel(t.Context(), "mock2", "verified-mock2")
	allow := func(ExecutionRun, TaskExecution) error { return nil }
	// The fallback selector binds the owner-approved plan scope.
	reader := e.planReader.(*MemoryPlanReader)
	p, err := reader.GetActivePlan(ctx, parent.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	p.State = plan.StateReady
	if p, err = p.Approve(time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	reader.AddPlan(p)
	first, err := e.OpenTaskLane(ctx, parent.RunID, "task-1", base)
	if err != nil {
		t.Fatal(err)
	}
	failLaneInStore(t, e, first.RunID)
	newer := advanceHead(t, parent.Provenance.RepoRoot)

	// Without a failover the next attempt keeps the original base.
	if _, err = e.OpenTaskLaneRework(ctx, parent.RunID, "task-1", newer, 1); !errors.Is(err, ErrRunConflict) || !strings.Contains(err.Error(), "durable task lane binding differs") {
		t.Fatalf("a rework without failover opened from another base: %v", err)
	}

	// A failed lane that does not carry the task's binding is foreign.
	failed := mustRun(t, e, first.RunID)
	genuine := failed.ParentBinding
	failed.ParentBinding = strings.Repeat("f", len(genuine))
	if err = e.store.UpdateRun(ctx, failed); err != nil {
		t.Fatal(err)
	}
	from := mustRun(t, e, parent.RunID).Tasks["task-1"].AssignedHarness
	if _, err = e.RebindTaskLaneApprovedProviderFailover(ctx, parent.RunID, mustRun(t, e, parent.RunID).Version, "task-1", from, "mock2", newer, "failover-1", allow); !errors.Is(err, ErrRunConflict) {
		t.Fatalf("a failover over a foreign lane binding was accepted: %v", err)
	}
	failed = mustRun(t, e, first.RunID)
	failed.ParentBinding = genuine
	if err = e.store.UpdateRun(ctx, failed); err != nil {
		t.Fatal(err)
	}

	rebound, err := e.RebindTaskLaneApprovedProviderFailover(ctx, parent.RunID, mustRun(t, e, parent.RunID).Version, "task-1", from, "mock2", newer, "failover-1", allow)
	if err != nil {
		t.Fatalf("failover from the newer base was refused: %v", err)
	}
	if record := rebound.TaskProviderSwitches["task-1"]; record.To != "mock2" || len(record.PriorBindings) != 1 || record.PriorBindings[0] != genuine {
		t.Fatalf("failover did not retire the failed binding: %+v", record)
	}
	second, err := e.OpenTaskLaneRework(ctx, parent.RunID, "task-1", newer, 1)
	if err != nil {
		t.Fatalf("the failed-over attempt was refused: %v", err)
	}
	latest := mustRun(t, e, parent.RunID)
	if second.BaseCommit != newer || second.Tasks["task-1"].AssignedHarness != "mock2" || second.ParentBinding != taskLaneBinding(latest, latest.Tasks["task-1"], newer) {
		t.Fatalf("failed-over attempt binding: base %s harness %s", second.BaseCommit, second.Tasks["task-1"].AssignedHarness)
	}
	if kept := mustRun(t, e, first.RunID); kept.BaseCommit != base || kept.Tasks["task-1"].State != TaskFailed {
		t.Fatal("the failed attempt was not retained")
	}
}
