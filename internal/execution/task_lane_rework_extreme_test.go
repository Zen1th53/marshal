//go:build !governed

package execution

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func registerWritingHarness(e *Engine) {
	e.RegisterHarness(NewMockHarness("mock", func(_ context.Context, task TaskExecution, _ ConstraintPackage, path string) (TaskResult, error) {
		if err := os.WriteFile(filepath.Join(path, task.TargetFiles[0]), []byte(task.TaskID+" "+task.RunID), 0600); err != nil {
			return TaskResult{}, err
		}
		return TaskResult{TaskID: task.TaskID, Success: true}, nil
	}))
}

func executeLane(t *testing.T, e *Engine, lane *ExecutionRun, base string) *ExecutionRun {
	t.Helper()
	run, err := e.ExecuteTaskBound(t.Context(), lane.RunID, lane.LaneTaskID, base)
	if err != nil {
		t.Fatal(err)
	}
	if run.Tasks[lane.LaneTaskID].State != TaskCompletedPendingVerify || run.Tasks[lane.LaneTaskID].ResultCommit == "" {
		t.Fatalf("lane %s did not complete: %s", lane.RunID, run.Tasks[lane.LaneTaskID].State)
	}
	return run
}

func TestExtremeTaskLaneReworkOpensFreshAttemptInOrder(t *testing.T) {
	e, parent, base := laneFixture(t)
	registerWritingHarness(e)
	first, err := e.OpenTaskLane(t.Context(), parent.RunID, "task-1", base)
	if err != nil {
		t.Fatal(err)
	}
	// An attempt that has not run is superseded by a rework, never run
	// beside it: TestExtremeTaskLaneReworkSupersedesUnstartedAttempt.
	firstDone := executeLane(t, e, first, base)
	if _, err := e.ExecuteTaskBound(t.Context(), first.RunID, "task-1", base); !errors.Is(err, ErrInvalidStateTransition) {
		t.Fatalf("completed lane unexpectedly re-executed: %v", err)
	}
	if _, err = e.OpenTaskLaneRework(t.Context(), parent.RunID, "task-2", base, 1); !errors.Is(err, ErrRunConflict) {
		t.Fatalf("rework opened without a previous attempt: %v", err)
	}
	second, err := e.OpenTaskLaneRework(t.Context(), parent.RunID, "task-1", base, 1)
	if err != nil {
		t.Fatal(err)
	}
	if second.RunID == first.RunID || second.RunID != taskLaneID(parent.RunID, "task-1", 1) || second.ParentBinding != first.ParentBinding || second.BaseCommit != base {
		t.Fatal("rework lane identity or binding differs")
	}
	if task := second.Tasks["task-1"]; task.State != TaskPending || task.Attempts != 0 || task.ResultCommit != "" {
		t.Fatalf("rework lane did not start fresh: %+v", task)
	}
	again, err := e.OpenTaskLaneRework(t.Context(), parent.RunID, "task-1", base, 1)
	if err != nil || again.RunID != second.RunID {
		t.Fatalf("rework replay was not idempotent: %v", err)
	}
	if _, err = e.OpenTaskLane(t.Context(), parent.RunID, "task-1", base); !errors.Is(err, ErrRunConflict) {
		t.Fatalf("superseded attempt reopened: %v", err)
	}
	// The superseded lane keeps its evidence but can no longer reserve calls.
	retained, err := e.GetRun(t.Context(), first.RunID)
	if err != nil || retained.Tasks["task-1"].ResultCommit != firstDone.Tasks["task-1"].ResultCommit {
		t.Fatal("previous attempt evidence was not retained")
	}
	if err = e.reserveTaskLaneAttempt(t.Context(), retained, retained.Tasks["task-1"]); !errors.Is(err, ErrRunBlocked) {
		t.Fatalf("superseded lane reserved a model call: %v", err)
	}
	executeLane(t, e, second, base)
	latest, err := e.GetRun(t.Context(), parent.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if latest.TaskLanes["task-1"] != second.RunID || latest.TaskLaneStates["task-1"] != TaskCompletedPendingVerify || latest.ParallelCallsReserved != 2 {
		t.Fatalf("parent does not follow the rework: lanes=%v states=%v reserved=%d", latest.TaskLanes, latest.TaskLaneStates, latest.ParallelCallsReserved)
	}
	attempts, err := e.taskLaneAttempts(t.Context(), parent.RunID, "task-1", mustRun(t, e, second.RunID))
	if err != nil || len(attempts) != 2 || attempts[0].RunID != first.RunID {
		t.Fatalf("parent budget lost the returned attempt: %v %d", err, len(attempts))
	}
}

func TestExtremeTaskLaneReworkReopensCompletedParent(t *testing.T) {
	e, parent, base := laneFixture(t)
	registerWritingHarness(e)
	for _, id := range []string{"task-1", "task-2", "task-3"} {
		lane, err := e.OpenTaskLane(t.Context(), parent.RunID, id, base)
		if err != nil {
			t.Fatal(err)
		}
		executeLane(t, e, lane, base)
	}
	done, err := e.GetRun(t.Context(), parent.RunID)
	if err != nil || done.State != RunDonePendingVerification {
		t.Fatalf("parent did not complete: %v %s", err, done.State)
	}
	if _, err = e.OpenTaskLane(t.Context(), parent.RunID, "task-2", base); !errors.Is(err, ErrRunInvalid) {
		t.Fatalf("a completed parent reopened without a rework: %v", err)
	}
	rework, err := e.OpenTaskLaneRework(t.Context(), parent.RunID, "task-2", base, 1)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := e.GetRun(t.Context(), parent.RunID)
	if err != nil || reopened.State != RunRunning || reopened.EndedAt != nil || reopened.TaskLanes["task-2"] != rework.RunID {
		t.Fatalf("parent did not reopen for the rework: %v %s", err, reopened.State)
	}
	executeLane(t, e, rework, base)
	final, err := e.GetRun(t.Context(), parent.RunID)
	if err != nil || final.State != RunDonePendingVerification {
		t.Fatalf("parent did not complete after the rework: %v %s", err, final.State)
	}
}

func mustRun(t *testing.T, e *Engine, id string) ExecutionRun {
	t.Helper()
	run, err := e.GetRun(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

// A lane whose attempt never started (its dispatch was refused before any
// worker ran) is cancelled when the next attempt opens, so the task is not
// left blocked behind it; an attempt that did run is never superseded.
func TestExtremeTaskLaneReworkSupersedesUnstartedAttempt(t *testing.T) {
	e, parent, base := laneFixture(t)
	registerWritingHarness(e)
	first, err := e.OpenTaskLane(t.Context(), parent.RunID, "task-1", base)
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.OpenTaskLaneRework(t.Context(), parent.RunID, "task-1", base, 1)
	if err != nil {
		t.Fatal(err)
	}
	if second.RunID != taskLaneID(parent.RunID, "task-1", 1) || second.Tasks["task-1"].State != TaskPending {
		t.Fatal("rework lane identity differs")
	}
	old := mustRun(t, e, first.RunID)
	if old.State != RunCancelled || old.Tasks["task-1"].State != TaskCancelled || old.Tasks["task-1"].LastFailureReason == "" {
		t.Fatalf("unstarted attempt was not superseded: %s %s", old.State, old.Tasks["task-1"].State)
	}
	latest := mustRun(t, e, parent.RunID)
	if latest.TaskLanes["task-1"] != second.RunID {
		t.Fatalf("parent does not follow the superseding attempt: %v", latest.TaskLanes)
	}
	done := executeLane(t, e, second, base)
	third, err := e.OpenTaskLaneRework(t.Context(), parent.RunID, "task-1", base, 2)
	if err != nil {
		t.Fatal(err)
	}
	if kept := mustRun(t, e, second.RunID); kept.State == RunCancelled || kept.Tasks["task-1"].ResultCommit != done.Tasks["task-1"].ResultCommit {
		t.Fatal("an attempt that ran was superseded")
	}
	if third.RunID != taskLaneID(parent.RunID, "task-1", 2) {
		t.Fatal("third attempt identity differs")
	}
}

// A lane left RUNNING by a restarted runtime (no executor owns it and no
// native turn is live) is paused with its evidence when a later attempt
// opens; attempt numbers whose dispatch was refused before a lane opened may
// be skipped, but an attempt never opens at or below the current one.
func TestExtremeTaskLaneReworkPausesOrphanAndAcceptsRefusedGap(t *testing.T) {
	e, parent, base := laneFixture(t)
	registerWritingHarness(e)
	first, err := e.OpenTaskLane(t.Context(), parent.RunID, "task-1", base)
	if err != nil {
		t.Fatal(err)
	}
	executeLane(t, e, first, base)
	second, err := e.OpenTaskLaneRework(t.Context(), parent.RunID, "task-1", base, 1)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a restart during the second attempt: the stored lane says
	// RUNNING with a started task, and no executor of this engine owns it.
	orphan := mustRun(t, e, second.RunID)
	task := orphan.Tasks["task-1"]
	now := time.Now().UTC()
	task.State = TaskRunning
	task.Attempts = 1
	task.StartedAt = &now
	orphan.Tasks["task-1"] = task
	orphan.State = RunRunning
	if err = e.store.UpdateRun(t.Context(), orphan); err != nil {
		t.Fatal(err)
	}
	if _, err = e.OpenTaskLaneRework(t.Context(), parent.RunID, "task-1", base, 1); !errors.Is(err, ErrRunBlocked) {
		t.Fatalf("the orphan attempt itself was resumed without reconciliation: %v", err)
	}
	if _, err = e.OpenTaskLane(t.Context(), parent.RunID, "task-1", base); !errors.Is(err, ErrRunConflict) {
		t.Fatalf("an attempt below the current one opened: %v", err)
	}
	// Attempts 2..4 were refused before their lanes opened.
	next, err := e.OpenTaskLaneRework(t.Context(), parent.RunID, "task-1", base, 5)
	if err != nil {
		t.Fatalf("a later attempt after an orphan and refused attempts did not open: %v", err)
	}
	if next.RunID != taskLaneID(parent.RunID, "task-1", 5) || next.Tasks["task-1"].State != TaskPending {
		t.Fatal("later attempt identity differs")
	}
	paused := mustRun(t, e, second.RunID)
	if paused.State != RunPaused || paused.Tasks["task-1"].State != TaskFailed || paused.Tasks["task-1"].LastFailureReason == "" || paused.Tasks["task-1"].StartedAt == nil {
		t.Fatalf("orphan attempt was not paused with its record: %s %s", paused.State, paused.Tasks["task-1"].State)
	}
	if latest := mustRun(t, e, parent.RunID); latest.TaskLanes["task-1"] != next.RunID {
		t.Fatalf("parent does not follow the later attempt: %v", latest.TaskLanes)
	}
	if _, err = e.OpenTaskLaneRework(t.Context(), parent.RunID, "task-1", base, 3); !errors.Is(err, ErrRunConflict) {
		t.Fatalf("an attempt below the current one opened: %v", err)
	}
	executeLane(t, e, next, base)
}

// A lane with a live native turn may still be running in its provider, so
// it is never paused by a later attempt.
func TestExtremeTaskLaneReworkKeepsLiveNativeTurnBlocked(t *testing.T) {
	e, parent, base := laneFixture(t)
	first, err := e.OpenTaskLane(t.Context(), parent.RunID, "task-1", base)
	if err != nil {
		t.Fatal(err)
	}
	stored := mustRun(t, e, first.RunID)
	task := stored.Tasks["task-1"]
	now := time.Now().UTC()
	task.State = TaskRunning
	task.Attempts = 1
	task.StartedAt = &now
	task.NativeTurn = &NativeTurnBinding{}
	stored.Tasks["task-1"] = task
	stored.State = RunRunning
	if err = e.store.UpdateRun(t.Context(), stored); err != nil {
		t.Fatal(err)
	}
	if _, err = e.OpenTaskLaneRework(t.Context(), parent.RunID, "task-1", base, 1); !errors.Is(err, ErrRunBlocked) {
		t.Fatalf("a lane with a live native turn was superseded: %v", err)
	}
	if kept := mustRun(t, e, first.RunID); kept.State != RunRunning {
		t.Fatalf("a lane with a live native turn changed state: %s", kept.State)
	}
}

// Marshal stopped (SIGTERM) while the worker of a task's first attempt ran:
// the reopened engine finds the lane RUNNING with no executor. The retry of
// that same attempt is still refused, but the interrupted attempt is closed
// (paused, its record kept, nothing claimed) and the next attempt then admits
// and runs. A lane this runtime still executes, one with a live native turn
// and one that is not the task's current attempt are never closed.
func TestExtremeOrphanTaskLaneClosedAfterRestartAdmitsNextAttempt(t *testing.T) {
	e, parent, base := laneFixture(t)
	first, err := e.OpenTaskLane(t.Context(), parent.RunID, "task-1", base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.ReconcileOrphanTaskLane(t.Context(), parent.RunID, "task-1", 0); !errors.Is(err, ErrRunConflict) {
		t.Fatalf("a lane that never started was closed as interrupted: %v", err)
	}
	stored := mustRun(t, e, first.RunID)
	task := stored.Tasks["task-1"]
	now := time.Now().UTC()
	task.State = TaskRunning
	task.Attempts = 1
	task.StartedAt = &now
	stored.Tasks["task-1"] = task
	stored.State = RunRunning
	stored.CurrentPhase = PhaseExecuting
	if err = e.store.UpdateRun(t.Context(), stored); err != nil {
		t.Fatal(err)
	}
	// This runtime still executes it: never closed.
	e.operationMu.Lock()
	e.activeRuns[first.RunID] = struct{}{}
	e.operationMu.Unlock()
	if _, err = e.ReconcileOrphanTaskLane(t.Context(), parent.RunID, "task-1", 0); !errors.Is(err, ErrRunBlocked) {
		t.Fatalf("a live lane was closed: %v", err)
	}
	e.operationMu.Lock()
	delete(e.activeRuns, first.RunID)
	e.operationMu.Unlock()

	// Restart: a fresh engine over the same durable store.
	store, err := NewFileRunStore(filepath.Join(e.cfg.ProjectRoot, ".marshal", "lane-test-runs"))
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := NewEngineWithReaders(e.cfg, store, nil, e.planReader, e.goalReader)
	if err != nil {
		t.Fatal(err)
	}
	registerWritingHarness(fresh)
	if _, err = fresh.OpenTaskLane(t.Context(), parent.RunID, "task-1", base); !errors.Is(err, ErrRunBlocked) || !strings.Contains(err.Error(), "orphan task lane") {
		t.Fatalf("the interrupted attempt was resumed without being closed: %v", err)
	}
	if _, err = fresh.ReconcileOrphanTaskLane(t.Context(), parent.RunID, "task-1", 1); !errors.Is(err, ErrRunConflict) {
		t.Fatalf("an attempt that is not the current one was closed: %v", err)
	}
	// A live native turn may still be running in its provider.
	native := mustRun(t, fresh, first.RunID)
	nativeTask := native.Tasks["task-1"]
	nativeTask.NativeTurn = &NativeTurnBinding{}
	native.Tasks["task-1"] = nativeTask
	if err = fresh.store.UpdateRun(t.Context(), native); err != nil {
		t.Fatal(err)
	}
	if _, err = fresh.ReconcileOrphanTaskLane(t.Context(), parent.RunID, "task-1", 0); !errors.Is(err, ErrRunConflict) {
		t.Fatalf("a lane with a live native turn was closed: %v", err)
	}
	native = mustRun(t, fresh, first.RunID)
	nativeTask = native.Tasks["task-1"]
	nativeTask.NativeTurn = nil
	native.Tasks["task-1"] = nativeTask
	if err = fresh.store.UpdateRun(t.Context(), native); err != nil {
		t.Fatal(err)
	}

	closed, err := fresh.ReconcileOrphanTaskLane(t.Context(), parent.RunID, "task-1", 0)
	if err != nil {
		t.Fatalf("the interrupted attempt was not closed: %v", err)
	}
	got := closed.Tasks["task-1"]
	if closed.State != RunPaused || got.State != TaskFailed || got.ResultCommit != "" || got.StartedAt == nil || got.Attempts != 1 || got.LastFailureReason == "" {
		t.Fatalf("closing claimed a result or lost the record: %s %+v", closed.State, got)
	}
	if latest := mustRun(t, fresh, parent.RunID); latest.TaskLanes["task-1"] != first.RunID {
		t.Fatalf("closing moved the parent pointer: %v", latest.TaskLanes)
	}
	next, err := fresh.OpenTaskLaneRework(t.Context(), parent.RunID, "task-1", base, 1)
	if err != nil {
		t.Fatalf("the next attempt was not admitted after closing: %v", err)
	}
	executeLane(t, fresh, next, base)
	if latest := mustRun(t, fresh, parent.RunID); latest.TaskLanes["task-1"] != next.RunID {
		t.Fatalf("parent does not follow the next attempt: %v", latest.TaskLanes)
	}
	if kept := mustRun(t, fresh, first.RunID); kept.State != RunPaused || kept.Tasks["task-1"].StartedAt == nil {
		t.Fatalf("the interrupted attempt's record changed: %s", kept.State)
	}
}

// Infrastructure retries, refused dispatches and orphan closes each advance a
// task's lane attempt number without being rework. A real run reached
// attempt 17 that way and every later attempt was refused as "task lane
// rework out of range". Attempt numbers well past the old bound open, close
// as orphans and run; only a number past the identity bound is refused.
func TestExtremeTaskLaneAttemptsPastOldReworkBoundStillOpen(t *testing.T) {
	e, parent, base := laneFixture(t)
	registerWritingHarness(e)
	first, err := e.OpenTaskLane(t.Context(), parent.RunID, "task-1", base)
	if err != nil {
		t.Fatal(err)
	}
	executeLane(t, e, first, base)
	interrupted, err := e.OpenTaskLaneRework(t.Context(), parent.RunID, "task-1", base, 17)
	if err != nil {
		t.Fatalf("attempt 17 after infrastructure retries was refused: %v", err)
	}
	orphan := mustRun(t, e, interrupted.RunID)
	task := orphan.Tasks["task-1"]
	now := time.Now().UTC()
	task.State, task.Attempts, task.StartedAt = TaskRunning, 1, &now
	orphan.Tasks["task-1"] = task
	orphan.State, orphan.CurrentPhase = RunRunning, PhaseExecuting
	if err = e.store.UpdateRun(t.Context(), orphan); err != nil {
		t.Fatal(err)
	}
	if _, err = e.ReconcileOrphanTaskLane(t.Context(), parent.RunID, "task-1", 17); err != nil {
		t.Fatalf("interrupted attempt 17 was not closed: %v", err)
	}
	next, err := e.OpenTaskLaneRework(t.Context(), parent.RunID, "task-1", base, 40)
	if err != nil {
		t.Fatalf("attempt 40 was refused: %v", err)
	}
	executeLane(t, e, next, base)
	if n, ok := TaskLaneAttempt(parent.RunID, "task-1", next.RunID); !ok || n != 40 {
		t.Fatalf("attempt 40 not recognised: %d %v", n, ok)
	}
	attempts, err := e.taskLaneAttempts(t.Context(), parent.RunID, "task-1", mustRun(t, e, next.RunID))
	if err != nil || len(attempts) != 3 {
		t.Fatalf("parent budget lost earlier attempts: %v %d", err, len(attempts))
	}
	if _, err = e.OpenTaskLaneRework(t.Context(), parent.RunID, "task-1", base, maxTaskLaneAttempts+1); !errors.Is(err, ErrRunInvalid) {
		t.Fatalf("an attempt past the identity bound opened: %v", err)
	}
}
