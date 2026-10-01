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
	_, _ = e.journal.Append(JournalEvent{RunID: runID, Actor: "MARSHAL_ENGINE", EventType: "RUN_CONTROL_" + operation, Summary: "operator requested " + operation})
	return RunControlResult{Run: run, Executing: e.IsExecuting(runID)}, nil
}

// IsExecuting reports whether an executor currently owns the run.
func (e *Engine) IsExecuting(runID string) bool {
	e.operationMu.Lock()
	defer e.operationMu.Unlock()
	_, running := e.activeRuns[runID]
	return running
}
