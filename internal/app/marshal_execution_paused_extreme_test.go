//go:build !governed

package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
)

// The live rv06 loop: the run's execution was left paused by the person and
// a restart dropped the session's pause hold. Each dispatch found the paused
// execution, parked the task as queued and dispatched it again, 31 times with
// no worker, until the run counted as stalled with nothing to resume. Now the
// first such dispatch stops the run for the person, saying what is paused;
// their resume resumes the paused part and the task runs.
func TestExtremePausedExecutionWithoutHoldStopsInsteadOfLooping(t *testing.T) {
	s, _, _ := parallelWaitFixture(t, 2)
	var mu sync.Mutex
	launches := map[string]int{}
	paused := true
	s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		id := req.Task.PlanTaskID
		mu.Lock()
		launches[id]++
		blocked := id == "b" && paused
		mu.Unlock()
		if blocked {
			return nil, fmt.Errorf("worker failed: %w", ErrProcess05Paused)
		}
		return nil, os.WriteFile(filepath.Join(req.Worktree, id+".txt"), []byte("done\n"), 0600)
	}}
	resumed := 0
	s.process05ResumePaused = func(context.Context, marshal.Run) error {
		mu.Lock()
		defer mu.Unlock()
		resumed++
		paused = false
		return nil
	}
	brief := func(marshal.Task, BriefContext) string { return "brief" }
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	run, _ := s.Execute(ctx, "par", brief, nil)
	for i := 0; i < 4 && run.Pause == nil && ctx.Err() == nil; i++ {
		run, _ = s.Execute(ctx, "par", brief, nil)
	}
	run, _, err := s.load(t.Context(), "par")
	if err != nil {
		t.Fatal(err)
	}
	if run.Pause == nil || run.Pause.Code != marshal.CodeExecutionPaused || run.Pause.ResumeState == "" || run.Pause.Detail == "" {
		t.Fatalf("run did not stop for the paused execution: %s %+v", run.State, run.Pause)
	}
	if b := run.Tasks[taskIndex(run, "b")]; b.State != marshal.Queued || taskReturns(b) != 0 {
		t.Fatalf("parked task %s returns %d", b.State, taskReturns(b))
	}
	// The stop is visible: an uncounted escalation event with its cause.
	decisions, err := s.Store.MarshalDecisions(t.Context(), "par")
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, e := range decisions {
		if e.TaskID == "b" && e.Type == events.EventTypeMarshalEscalated && e.Data["code"] == marshal.CodeExecutionPaused && e.Data["counted"] == false && e.Data["detail"] != "" {
			seen = true
		}
	}
	if !seen {
		t.Fatal("the paused-execution stop recorded no event")
	}
	mu.Lock()
	// The sibling's merge in the same round may take the run on once more
	// before it stops; it never loops (the live run dispatched 31 times).
	if launches["b"] < 1 || launches["b"] > 2 {
		mu.Unlock()
		t.Fatalf("the paused task was dispatched %d times, want at most 2", launches["b"])
	}
	mu.Unlock()

	if run, err = s.Resume(t.Context(), "par"); err != nil {
		t.Fatalf("the person's resume of the stop failed: %v", err)
	}
	if resumed != 1 || run.State == marshal.AwaitingUser {
		t.Fatalf("resume did not resume the paused execution: resumed %d, %s", resumed, run.State)
	}
	run, err = s.Execute(ctx, "par", brief, nil)
	for i := 0; err == nil && i < 4 && run.Tasks[taskIndex(run, "b")].State != marshal.Merged; i++ {
		run, err = s.Execute(ctx, "par", brief, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	if b := run.Tasks[taskIndex(run, "b")]; b.State != marshal.Merged {
		t.Fatalf("resumed task ended %s", b.State)
	}
}

// While this session's own pause holds the task, a parked task waits for
// that hold as before: no stop is written.
func TestExtremePausedExecutionUnderHoldOnlyParks(t *testing.T) {
	s, _, _ := parallelWaitFixture(t, 2)
	s.HeldTask = func(string, string) bool { return true }
	run, rev, err := s.load(t.Context(), "par")
	if err != nil {
		t.Fatal(err)
	}
	i := taskIndex(run, "b")
	run.Tasks[i].State = marshal.Dispatched
	if err = s.save(t.Context(), "par", run, rev); err != nil {
		t.Fatal(err)
	}
	handled, err := s.parkPausedTask(t.Context(), "par", MarshalDispatch{TaskID: "b"}, fmt.Errorf("worker failed: %w", ErrProcess05Paused))
	if !handled || err != nil {
		t.Fatalf("park: %v %v", handled, err)
	}
	run, _, err = s.load(t.Context(), "par")
	if err != nil {
		t.Fatal(err)
	}
	if run.Pause != nil || run.Tasks[i].State != marshal.Queued {
		t.Fatalf("held task: %s pause %+v", run.Tasks[i].State, run.Pause)
	}
}

// A stall written by v0.3.7 (no resume state, no escalated task: the live
// rv06 card "resolve the escalation", with nothing escalated) is resumable by
// the person; a stall with an escalated task still needs that task resolved.
func TestExtremeStallWithoutEscalatedTaskResumes(t *testing.T) {
	s, _, _ := parallelWaitFixture(t, 2)
	stall := func(escalated bool) {
		run, rev, err := s.load(t.Context(), "par")
		if err != nil {
			t.Fatal(err)
		}
		if escalated {
			run.Tasks[taskIndex(run, "b")].State = marshal.Escalated
		}
		pauseMarshal(&run, marshal.Pause{Reason: "the run stopped making progress", Resolutions: []string{"resolve the escalation or amend the plan"}, Code: marshal.CodeRunStalled, Action: marshal.PauseResume})
		if err = s.save(t.Context(), "par", run, rev); err != nil {
			t.Fatal(err)
		}
	}
	stall(true)
	if _, err := s.Resume(t.Context(), "par"); err == nil {
		t.Fatal("a stall with an escalated task resumed")
	}
	run, rev, err := s.load(t.Context(), "par")
	if err != nil {
		t.Fatal(err)
	}
	run.Tasks[taskIndex(run, "b")].State = marshal.Queued
	if err = s.save(t.Context(), "par", run, rev); err != nil {
		t.Fatal(err)
	}
	stall(false)
	if run, err = s.Resume(t.Context(), "par"); err != nil || run.State == marshal.AwaitingUser || run.Pause != nil {
		t.Fatalf("a stall with nothing escalated did not resume: %v %s %+v", err, run.State, run.Pause)
	}
}
