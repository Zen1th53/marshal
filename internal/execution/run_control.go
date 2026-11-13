package execution

import (
	"context"
	"fmt"
	"time"
)

// RunControlResult is the run after a control request and whether its
// executor has settled. A pause or cancel is recorded at once, but a task
// already in a provider turn finishes that turn first: until the executor
// returns, the run is still settling.
type RunControlResult struct {
	Run       ExecutionRun
	Executing bool
}

// ControlRun records pause, resume or cancel on the exact run version the
// operator saw. The execution loop re-reads the run at every task boundary
// and stops dispatching once it is no longer RUNNING, so pause and cancel
// take effect there; resume validates the goal binding again before the run
// may dispatch. It never kills a process itself.
func (e *Engine) ControlRun(ctx context.Context, runID, operation string, expectedVersion int64) (RunControlResult, error) {
	e.operationMu.Lock()
	defer e.operationMu.Unlock()
	return e.controlRunLocked(ctx, runID, operation, expectedVersion)
}

// controlRunLocked is ControlRun for a caller already holding operationMu,
// such as a parent run controlling its task lanes.
func (e *Engine) controlRunLocked(ctx context.Context, runID, operation string, expectedVersion int64) (RunControlResult, error) {
	run, err := e.store.GetRun(ctx, runID)
	if err != nil {
		return RunControlResult{}, err
	}
	if run.Version != expectedVersion {
		return RunControlResult{}, fmt.Errorf("%w: run %s moved from version %d to %d", ErrRunConflict, runID, expectedVersion, run.Version)
	}
	now := time.Now().UTC()
	switch operation {
	case "pause":
		err = run.Pause(now)
	case "resume":
		if err = e.ValidateRunGoal(ctx, run); err == nil {
			err = run.Resume(now)
		}
	case "cancel":
		if run.State.IsTerminal() {
			err = fmt.Errorf("%w: run %s is already %s", ErrInvalidStateTransition, runID, run.State)
		} else {
			err = run.Cancel(now)
		}
	default:
		err = fmt.Errorf("%w: unknown run control %q", ErrRunInvalid, operation)
	}
	if err != nil {
		return RunControlResult{}, err
	}
	if err := e.persistRun(ctx, &run); err != nil {
		return RunControlResult{}, err
	}
	if err := e.controlTaskLanes(ctx, run, operation); err != nil {
		// operationMu is held here: read the executors directly, as below,
		// not through IsExecuting, which takes it again.
		_, executing := e.activeRuns[runID]
		return RunControlResult{Run: run, Executing: executing || e.taskLanesExecuting(runID)}, err
	}
	if operation == "cancel" {
		if cancel := e.turnCancels[runID]; cancel != nil {
			cancel()
		}
	}
	_, _ = e.journal.Append(JournalEvent{RunID: runID, Actor: "MARSHAL_ENGINE", EventType: "RUN_CONTROL_" + operation, Summary: "operator requested " + operation})
	// A parent run is still executing while any of its task lanes is.
	_, executing := e.activeRuns[runID]
	executing = executing || e.taskLanesExecuting(runID)
	if operation == "resume" && !executing && workComplete(run) {
		// Its last task finished while it was paused and no executor is
		// left to end it: it ends now as the execution loop ends a run.
		if err := e.completePendingVerification(ctx, &run); err != nil {
			return RunControlResult{Run: run}, err
		}
	}
	return RunControlResult{Run: run, Executing: executing}, nil
}

// workComplete reports whether every task of run completed pending
// verification: its own tasks, or for a task lane parent the task of every
// lane, as the parent's lane summary records it.
func workComplete(run ExecutionRun) bool {
	if len(run.Tasks) == 0 {
		return false
	}
	if len(run.TaskLanes) > 0 && len(run.TaskLanes) != len(run.Tasks) {
		return false
	}
	for id, task := range run.Tasks {
		state := task.State
		if len(run.TaskLanes) > 0 {
			state = run.TaskLaneStates[id]
		}
		if state != TaskCompletedPendingVerify {
			return false
		}
	}
	return true
}

// SettleCompletedRun ends a run whose every task completed but that no
// executor owns any more and that is not done: one paused while its last
// task's turn ran (the turn finishes, the run stays paused) and never
// resumed, or one resumed just as its executor returned. A paused run is
// resumed and a running one completed, through the same transitions as an
// operator resume and the execution loop. It reports whether the run
// changed; any other run is left as it is.
func (e *Engine) SettleCompletedRun(ctx context.Context, runID string) (bool, error) {
	e.operationMu.Lock()
	defer e.operationMu.Unlock()
	if _, active := e.activeRuns[runID]; active || e.taskLanesExecuting(runID) {
		return false, nil
	}
	run, err := e.store.GetRun(ctx, runID)
	if err != nil {
		return false, err
	}
	if (run.State != RunPaused && run.State != RunRunning) || !workComplete(run) {
		return false, nil
	}
	if run.State == RunPaused {
		_, err = e.controlRunLocked(ctx, runID, "resume", run.Version)
		return err == nil, err
	}
	return true, e.completePendingVerification(ctx, &run)
}

// IsExecuting reports whether an executor currently owns the run.
func (e *Engine) IsExecuting(runID string) bool {
	e.operationMu.Lock()
	defer e.operationMu.Unlock()
	_, running := e.activeRuns[runID]
	return running || e.taskLanesExecuting(runID)
}
