package execution

import (
	"context"
	"time"
)

// Standalone engines preserve their existing dispatch. A composed runtime can
// only constrain it at construction; registration cannot grant an exception.
func (e *Engine) admitWorkerHarness(name string) (WorkerHarness, bool, error) {
	if e.harnessAdmission == nil {
		return nil, false, nil
	}
	if name == "" {
		name = "unbound"
	}
	h, err := e.GetHarness(name)
	if err != nil {
		return nil, true, err
	}
	if err := e.harnessAdmission(h); err != nil {
		return nil, true, err
	}
	return h, true, nil
}

func (e *Engine) refuseWorkerHarness(ctx context.Context, run *ExecutionRun, task TaskExecution, cause error) error {
	task.State = TaskFailed
	task.LastFailureReason = cause.Error()
	run.Tasks[task.TaskID] = task
	run.State = RunFailed
	run.CurrentPhase = PhaseTerminated
	run.Failures = append(run.Failures, RunFailure{TaskID: task.TaskID, Stage: "HARNESS_ADMISSION", Reason: task.LastFailureReason, Timestamp: time.Now().UTC(), Recoverable: false})
	if err := e.persistRun(ctx, run); err != nil {
		return err
	}
	_, err := e.journal.Append(JournalEvent{RunID: run.RunID, TaskID: task.TaskID, Actor: "MARSHAL_ENGINE", EventType: "HARNESS_ADMISSION_REFUSED", Summary: task.LastFailureReason})
	if err != nil {
		return err
	}
	return cause
}
