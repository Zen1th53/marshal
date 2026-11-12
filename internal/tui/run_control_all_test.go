package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/statepath"
)

// runControlAllWorkspace is a workspace on a real runtime whose session has
// the given runs, with per-run control applied straight to the engine (the
// state change /pause run:<id> records, without resume restarting
// execution) and failing for the runs in fail.
func runControlAllWorkspace(t *testing.T, runs []execution.ExecutionRun, fail ...string) (*Workspace, context.Context) {
	t.Helper()
	_, w, ctx := acceptanceWorkspace(t)
	store, err := execution.NewFileRunStore(filepath.Join(w.workDir, statepath.DirName, "execution", "runs"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, run := range runs {
		run.ProjectID, run.SessionID, run.StartedAt, run.UpdatedAt = projectid.ID(w.runtime.ProjectIdentity()), w.sessionID, now, now
		if err := store.CreateRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}
	installRunControlSeam(t, w, fail...)
	return w, ctx
}

// installRunControlSeam applies per-run control straight to the engine (the
// state change /pause run:<id> records, without resume restarting
// execution), failing for the runs in fail, and gives the workspace a
// Marshal run whose dispatch the pause holds.
func installRunControlSeam(t *testing.T, w *Workspace, fail ...string) {
	t.Helper()
	old := runControlEach
	t.Cleanup(func() { runControlEach = old })
	runControlEach = func(ctx context.Context, r *app.Runtime, e app.CommandEnvelope, operation string) (execution.RunControlResult, error) {
		id := strings.TrimPrefix(e.TargetID, "run:")
		for _, f := range fail {
			if f == id {
				return execution.RunControlResult{}, fmt.Errorf("%w: run %s moved", execution.ErrRunConflict, id)
			}
		}
		return r.Execution().Engine().ControlRun(ctx, id, operation, e.ExpectedVersion)
	}
	m := w.marshalSession()
	m.mu.Lock()
	m.runID = "RUN-marshal"
	m.mu.Unlock()
}

func runStates(t *testing.T, w *Workspace, ctx context.Context) map[string]execution.RunState {
	t.Helper()
	_, runs, err := w.cmd.sessionRuns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]execution.RunState{}
	for _, run := range runs {
		out[run.RunID] = run.State
	}
	return out
}

// The palette's "pause the run" pauses every running run of the session, and
// holds the Marshal's dispatch; "resume the run" resumes them all and
// releases it.
func TestPauseAllPausesEveryRun(t *testing.T) {
	w, ctx := runControlAllWorkspace(t, []execution.ExecutionRun{
		{RunID: "RUN-a", State: execution.RunRunning},
		{RunID: "RUN-b", State: execution.RunNeedsApproval},
		{RunID: "RUN-c", State: execution.RunRunning},
	})
	checkPauseResumeAll(t, w, ctx, 3, nil)
}

// checkPauseResumeAll pauses every run of the session from the palette's
// command, checks the Marshal's dispatch is held, resumes them all and
// checks it is released. n is the number of tasks the line counts; the runs
// in marshal stay paused on resume, left for the Marshal to continue.
func checkPauseResumeAll(t *testing.T, w *Workspace, ctx context.Context, n int, marshal map[string]bool) {
	t.Helper()
	w.noteRunControl(ctx)
	if w.runPausable.Load() == 0 {
		t.Fatal("no run is pausable before the pause")
	}
	out, err := w.ExecuteCommand(ctx, "/pause all")
	if want := fmt.Sprintf("Paused %d running tasks.", n); err != nil || out != want {
		t.Fatalf("/pause all: %q %v", out, err)
	}
	for id, state := range runStates(t, w, ctx) {
		if state != execution.RunPaused {
			t.Errorf("%s is %s after pause all", id, state)
		}
	}
	m := w.marshalSession()
	if !m.isHeldFor("RUN-marshal", "T-04") {
		t.Fatal("the Marshal may still dispatch a new task while its lanes are paused")
	}
	if _, ok := m.claimDispatch("RUN-marshal", "T-04"); ok {
		t.Fatal("a dispatch claim passed the pause hold")
	}
	if w.runPausable.Load() != 0 || w.runResumable.Load() == 0 {
		t.Fatalf("after pause: pausable %d resumable %d", w.runPausable.Load(), w.runResumable.Load())
	}
	woken := m.holdChanged()
	out, err = w.ExecuteCommand(ctx, "/resume all")
	if want := fmt.Sprintf("Resumed %d tasks.", n); err != nil || out != want {
		t.Fatalf("/resume all: %q %v", out, err)
	}
	for id, state := range runStates(t, w, ctx) {
		if want := map[bool]execution.RunState{false: execution.RunRunning, true: execution.RunPaused}[marshal[id]]; state != want {
			t.Errorf("%s is %s after resume all, want %s", id, state, want)
		}
	}
	if w.runResumable.Load() != 0 {
		t.Fatalf("after resume the palette still offers resume for %d runs", w.runResumable.Load())
	}
	select {
	case <-woken:
	default:
		t.Fatal("resume did not wake the Marshal waiting on its hold")
	}
	if m.isHeldFor("RUN-marshal", "T-04") {
		t.Fatal("resume left the Marshal's dispatch held")
	}
	if out, _ := w.ExecuteCommand(ctx, "/resume all"); out != "Nothing is paused to resume." {
		t.Fatalf("second resume: %q", out)
	}
}

// A run that cannot be paused is named in plain words; the others stay
// paused. One run is still controlled by its id.
func TestPauseAllNamesWhatFailed(t *testing.T) {
	w, ctx := runControlAllWorkspace(t, []execution.ExecutionRun{
		{RunID: "RUN-x", State: execution.RunRunning},
		{RunID: "RUN-y", State: execution.RunRunning},
		{RunID: "RUN-z", State: execution.RunRunning},
	}, "RUN-y")
	out, err := w.ExecuteCommand(ctx, "/pause all")
	want := "Paused 2 running tasks; one part of the run could not be paused (it changed meanwhile; try again)."
	if err != nil || out != want {
		t.Fatalf("/pause all: %q %v, want %q", out, err, want)
	}
	states := runStates(t, w, ctx)
	if states["RUN-x"] != execution.RunPaused || states["RUN-z"] != execution.RunPaused || states["RUN-y"] != execution.RunRunning {
		t.Fatalf("states after a partial pause: %v", states)
	}
	if !w.marshalSession().isHeldFor("RUN-marshal", "T-01") {
		t.Fatal("a partial pause did not hold the Marshal's dispatch")
	}
	// Resuming the paused ones one by one releases the hold once none is
	// paused.
	for _, id := range []string{"RUN-x", "RUN-z"} {
		if out, err := w.ExecuteCommand(ctx, "/resume run:"+id); err != nil || !strings.Contains(out, "resumed") {
			t.Fatalf("/resume run:%s: %q %v", id, out, err)
		}
	}
	if w.marshalSession().isHeldFor("RUN-marshal", "T-01") {
		t.Fatal("resuming every paused run left the Marshal held")
	}
}

// With nothing running, pause says so and holds nothing.
func TestPauseAllWithNothingRunning(t *testing.T) {
	w, ctx := runControlAllWorkspace(t, []execution.ExecutionRun{{RunID: "RUN-p", State: execution.RunPaused}})
	if out, _ := w.ExecuteCommand(ctx, "/pause all"); out != "Nothing is running to pause." {
		t.Fatalf("/pause all: %q", out)
	}
	if w.marshalSession().isHeldFor("RUN-marshal", "T-01") {
		t.Fatal("pausing nothing held the Marshal")
	}
}

// Pausing one run by its id holds the Marshal's dispatch too, so the Marshal
// does not start the paused work again behind the person's back.
func TestPauseOneRunHoldsDispatch(t *testing.T) {
	w, ctx := runControlAllWorkspace(t, []execution.ExecutionRun{{RunID: "RUN-s", State: execution.RunRunning}, {RunID: "RUN-t", State: execution.RunRunning}})
	if out, err := w.ExecuteCommand(ctx, "/pause run:RUN-s"); err != nil || !strings.Contains(out, "PAUSED") {
		t.Fatalf("/pause run:RUN-s: %q %v", out, err)
	}
	if !w.marshalSession().isHeldFor("RUN-marshal", "T-01") {
		t.Fatal("a single pause left the Marshal free to dispatch")
	}
}
