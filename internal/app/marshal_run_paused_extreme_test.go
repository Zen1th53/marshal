//go:build !governed

package app

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/plan"
)

// The person pauses the run while task a's lane has not started its turn and
// task b's lane is mid-turn. The pause never breaks the run: a goes back to
// waiting with no rework, b finishes its turn and is collected, the Marshal
// holds dispatch until the person resumes, then continues a and the run
// completes.
func TestExtremePauseDuringLanesNeverBreaksRun(t *testing.T) {
	s, _ := setupExtremeService(t, 2)
	fake := s.Model.(marshalFakeModel)
	draft := fake.draft
	for i := range draft.Tasks {
		draft.Tasks[i].DependsOn = nil
		draft.Plan.Tasks[i].DependsOn = nil
	}
	graph, err := plan.BuildGraph(draft.Plan.Tasks)
	if err != nil {
		t.Fatal(err)
	}
	draft.Plan.Graph = graph
	s.Model = marshalFakeModel{draft: draft, review: fake.review}

	var mu sync.Mutex
	held := false
	changed := make(chan struct{})
	launches := map[string]int{}
	started := make(chan string, 4)
	paused := make(chan struct{})
	s.HeldTask = func(string) bool { mu.Lock(); defer mu.Unlock(); return held }
	s.HoldChanged = func() <-chan struct{} { mu.Lock(); defer mu.Unlock(); return changed }
	s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		id := req.Task.PlanTaskID
		mu.Lock()
		launches[id]++
		first := launches[id] == 1
		mu.Unlock()
		started <- id
		if first {
			<-paused
			if id == "a" {
				// a's lane was paused before its turn began.
				return nil, ErrProcess05Paused
			}
			// b was mid-turn: its turn finishes despite the pause.
		}
		return nil, os.WriteFile(filepath.Join(req.Worktree, id+".txt"), []byte("done\n"), 0600)
	}}
	if _, err := s.StartPlanning(t.Context(), "par", "write", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(t.Context(), "par"); err != nil {
		t.Fatal(err)
	}
	run, rev, err := s.load(t.Context(), "par")
	if err != nil {
		t.Fatal(err)
	}
	run.Process05Bound = true
	bindPrivateProcess05Tier(&run, plan.ExecutionPlan{Mode: plan.ModeUltra})
	run.Settings.GlobalCap = 2
	if err := s.save(t.Context(), "par", run, rev); err != nil {
		t.Fatal(err)
	}

	type result struct {
		run marshal.Run
		err error
	}
	done := make(chan result, 1)
	go func() {
		r, err := s.Execute(t.Context(), "par", func(marshal.Task, BriefContext) string { return "brief" }, nil)
		done <- result{r, err}
	}()
	for range 2 {
		select {
		case <-started:
		case <-time.After(10 * time.Second):
			t.Fatal("both lanes did not start")
		}
	}
	// The person pauses: the TUI holds dispatch, and the lanes stop.
	mu.Lock()
	held = true
	mu.Unlock()
	close(paused)

	// a goes back to waiting and b is collected; nothing new launches.
	deadline := time.Now().Add(10 * time.Second)
	for {
		now, _, err := s.load(t.Context(), "par")
		if err != nil {
			t.Fatal(err)
		}
		a, b := now.Tasks[0], now.Tasks[1]
		if a.State == marshal.Queued && (b.State == marshal.Accepted || b.State == marshal.Merged) {
			if len(a.ReturnsByAgent) != 0 {
				t.Fatalf("the pause counted as rework: %+v", a)
			}
			break
		}
		select {
		case r := <-done:
			t.Fatalf("the run ended while paused: %s %v", r.run.State, r.err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("paused tasks did not settle: a=%s b=%s", a.State, b.State)
		}
		time.Sleep(20 * time.Millisecond)
	}
	select {
	case id := <-started:
		t.Fatalf("task %s launched while the run was paused", id)
	case <-time.After(300 * time.Millisecond):
	}

	// The person resumes: the hold is released and a continues.
	mu.Lock()
	held = false
	close(changed)
	changed = make(chan struct{})
	mu.Unlock()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("the run failed after resume: %v", r.err)
		}
		for _, task := range r.run.Tasks {
			if task.State != marshal.Merged || len(task.ReturnsByAgent) != 0 {
				t.Fatalf("task %s ended %s (returns %v) after resume", task.PlanTaskID, task.State, task.ReturnsByAgent)
			}
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the run did not complete after resume")
	}
	mu.Lock()
	defer mu.Unlock()
	if launches["a"] != 2 || launches["b"] != 1 {
		t.Fatalf("launches %v, want a twice (paused, then continued) and b once", launches)
	}
}
