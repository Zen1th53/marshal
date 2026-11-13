package execution

import (
	"testing"
)

// pausedRunFixture is the budget fixture's run paused with the given task
// states, task b selected, as a run paused while b's turn ran is left.
func pausedRunFixture(t *testing.T, states map[string]TaskExecutionState) (*Engine, ExecutionRun) {
	t.Helper()
	e, _, run := budgetDispatchEngine(t, nil)
	stored, err := e.GetRun(t.Context(), run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	for id, state := range states {
		stored.Tasks[id] = TaskExecution{RunID: stored.RunID, TaskID: id, State: state}
	}
	stored.State, stored.CurrentPhase, stored.SelectedTask = RunPaused, PhasePaused, "b"
	if err = e.persistRun(t.Context(), &stored); err != nil {
		t.Fatal(err)
	}
	return e, stored
}

// A run paused while its last task's turn ran keeps its executor's pause
// after that turn finishes: nothing is left to run, and no executor is left
// to end it. Resuming it ends it as the execution loop ends a run never
// paused: done pending verification, no task selected.
func TestResumeOfFinishedPausedRunCompletesIt(t *testing.T) {
	e, paused := pausedRunFixture(t, map[string]TaskExecutionState{"a": TaskCompletedPendingVerify, "b": TaskCompletedPendingVerify})
	result, err := e.ControlRun(t.Context(), paused.RunID, "resume", paused.Version)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := e.GetRun(t.Context(), paused.RunID)
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range []ExecutionRun{result.Run, stored} {
		if run.State != RunDonePendingVerification || run.CurrentPhase != PhaseCompletedPending || run.SelectedTask != "" || run.EndedAt == nil {
			t.Fatalf("resumed finished run ended %s/%s selected=%q", run.State, run.CurrentPhase, run.SelectedTask)
		}
	}
}

// A paused run with work left resumes to running, as before.
func TestResumeOfUnfinishedPausedRunRuns(t *testing.T) {
	e, paused := pausedRunFixture(t, map[string]TaskExecutionState{"a": TaskCompletedPendingVerify, "b": TaskPending})
	result, err := e.ControlRun(t.Context(), paused.RunID, "resume", paused.Version)
	if err != nil || result.Run.State != RunRunning || result.Run.CurrentPhase != PhaseExecuting {
		t.Fatalf("resume: %s/%s %v", result.Run.State, result.Run.CurrentPhase, err)
	}
	if settled, err := e.SettleCompletedRun(t.Context(), paused.RunID); err != nil || settled {
		t.Fatalf("a run with work left was settled: %v %v", settled, err)
	}
}

// SettleCompletedRun ends only a finished run left paused or running with
// no executor, and leaves any other alone.
func TestSettleCompletedRun(t *testing.T) {
	e, paused := pausedRunFixture(t, map[string]TaskExecutionState{"b": TaskPending})
	if settled, err := e.SettleCompletedRun(t.Context(), paused.RunID); err != nil || settled {
		t.Fatalf("an unfinished paused run was settled: %v %v", settled, err)
	}
	if run, _ := e.GetRun(t.Context(), paused.RunID); run.State != RunPaused {
		t.Fatalf("an unfinished paused run moved to %s", run.State)
	}
	for _, from := range []RunState{RunPaused, RunRunning} {
		e, run := pausedRunFixture(t, map[string]TaskExecutionState{"b": TaskCompletedPendingVerify})
		run.State = from
		if err := e.persistRun(t.Context(), &run); err != nil {
			t.Fatal(err)
		}
		if settled, err := e.SettleCompletedRun(t.Context(), run.RunID); err != nil || !settled {
			t.Fatalf("%s finished run not settled: %v %v", from, settled, err)
		}
		done, err := e.GetRun(t.Context(), run.RunID)
		if err != nil || done.State != RunDonePendingVerification || done.SelectedTask != "" {
			t.Fatalf("%s finished run ended %s selected=%q %v", from, done.State, done.SelectedTask, err)
		}
		if settled, err := e.SettleCompletedRun(t.Context(), run.RunID); err != nil || settled {
			t.Fatalf("a done run was settled again: %v %v", settled, err)
		}
	}
}
