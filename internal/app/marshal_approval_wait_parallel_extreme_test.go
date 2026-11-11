//go:build !governed

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/plan"
)

// parallelWaitFixture approves n independent tasks as an ULTRA Process05 plan
// that runs at most two lanes at once. Each task in waits asks for its own
// exact approval on its first launch and finishes once that is decided.
func parallelWaitFixture(t *testing.T, n int, waits ...string) (*MarshalService, map[string]bool, *sync.Mutex) {
	t.Helper()
	s, _ := setupExtremeService(t, n)
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
	pending := map[string]bool{}
	launches := map[string]int{}
	for _, id := range waits {
		pending[id] = true
	}
	s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		id := req.Task.PlanTaskID
		mu.Lock()
		launches[id]++
		first, waits := launches[id] == 1, pending[id]
		mu.Unlock()
		if first && waits {
			return nil, process05ApprovalRequired("lane-"+id, id, "approval-"+id)
		}
		if waits {
			t.Errorf("task %s relaunched while its approval was pending", id)
		}
		return nil, os.WriteFile(filepath.Join(req.Worktree, id+".txt"), []byte("done\n"), 0600)
	}}
	s.process05WaitReceipt = func(_ context.Context, _ marshal.Run, task marshal.Task, runID, approvalID string) (bool, error) {
		if runID != "lane-"+task.PlanTaskID || approvalID != "approval-"+task.PlanTaskID {
			t.Errorf("unbound continuation for %s: %s %s", task.PlanTaskID, runID, approvalID)
		}
		mu.Lock()
		defer mu.Unlock()
		return pending[task.PlanTaskID], nil
	}
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
	if !parallelProcess05Waits(s.Gate, run) {
		t.Fatal("fixture is not a parallel ULTRA Process05 run")
	}
	return s, pending, &mu
}

func TestExtremeParallelDispatchPassesTaskApprovalWait(t *testing.T) {
	s, pending, mu := parallelWaitFixture(t, 3, "a")
	run, err := s.Execute(t.Context(), "par", func(marshal.Task, BriefContext) string { return "brief" }, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// a and b fill both lanes; a's wait frees its lane, so c still runs.
	if run.State != marshal.AwaitingUser || run.Pause == nil || !strings.Contains(run.Pause.Detail, "approval-a") || run.Pause.Reason != "A task is waiting for the Marshal's go-ahead." || run.Pause.ResumeState != marshal.Dispatching {
		t.Fatalf("run did not pause on the remaining wait: %+v", run)
	}
	a, b, c := run.Tasks[0], run.Tasks[1], run.Tasks[2]
	if a.State != marshal.Queued || a.Process05WaitApprovalID != "approval-a" || a.Process05WaitRunID != "lane-a" || len(a.ReturnsByAgent) != 0 {
		t.Fatalf("waiting task lost its binding or became rework: %+v", a)
	}
	if b.State != marshal.Accepted || c.State != marshal.Accepted {
		t.Fatalf("an approval wait held other lanes: b=%s c=%s", b.State, c.State)
	}
	if waiting, err := s.checkPrivateProcess05Approval(t.Context(), run); err != nil || !waiting {
		t.Fatalf("resume admitted before any decision: %v %v", waiting, err)
	}
	mu.Lock()
	pending["a"] = false
	mu.Unlock()
	resumed, err := s.Resume(t.Context(), "par")
	if err != nil || resumed.State != marshal.Dispatching {
		t.Fatalf("decided approval did not resume: %s %v", resumed.State, err)
	}
	final, err := s.Execute(t.Context(), "par", func(marshal.Task, BriefContext) string { return "brief" }, nil)
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	for _, task := range final.Tasks {
		if task.State != marshal.Merged {
			t.Fatalf("task %s ended %s after its approval", task.PlanTaskID, task.State)
		}
	}
}

func TestExtremeParallelResumeContinuesDecidedWaitPastPendingOne(t *testing.T) {
	s, pending, mu := parallelWaitFixture(t, 2, "a", "b")
	brief := func(marshal.Task, BriefContext) string { return "brief" }
	run, err := s.Execute(t.Context(), "par", brief, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if run.State != marshal.AwaitingUser || run.Pause == nil || !strings.Contains(run.Pause.Detail, "approval-a") || !strings.Contains(run.Pause.Detail, "approval-b") || run.Pause.Reason != "2 tasks are waiting for the Marshal's go-ahead." {
		t.Fatalf("pause does not name both waits: %+v", run.Pause)
	}
	mu.Lock()
	pending["b"] = false
	mu.Unlock()
	if _, err := s.Resume(t.Context(), "par"); err != nil {
		t.Fatalf("resume with one decided wait: %v", err)
	}
	run, err = s.Execute(t.Context(), "par", brief, nil)
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	// Plan order merges b only after a, so b is accepted and a still waits.
	if run.State != marshal.AwaitingUser || run.Tasks[1].State != marshal.Accepted || run.Tasks[0].State != marshal.Queued || run.Tasks[0].Process05WaitApprovalID != "approval-a" {
		t.Fatalf("decided lane did not continue past the pending one: %+v", run)
	}
	if strings.Contains(run.Pause.Detail, "approval-b") || !strings.Contains(run.Pause.Detail, "approval-a") {
		t.Fatalf("pause detail names a decided wait: %s", run.Pause.Detail)
	}
}

func TestExtremeSerialProcess05WaitStillPausesRun(t *testing.T) {
	s, _ := marshalFixture(t, 2)
	if _, err := s.StartPlanning(t.Context(), "serial", "write", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(t.Context(), "serial"); err != nil {
		t.Fatal(err)
	}
	run, rev, err := s.load(t.Context(), "serial")
	if err != nil {
		t.Fatal(err)
	}
	run.Process05Bound, run.State, run.Tasks[0].State = true, marshal.Dispatching, marshal.Dispatched
	if err := s.save(t.Context(), "serial", run, rev); err != nil {
		t.Fatal(err)
	}
	s.process05WaitReceipt = func(context.Context, marshal.Run, marshal.Task, string, string) (bool, error) { return true, nil }
	if handled, err := s.pausePrivateProcess05Approval(t.Context(), "serial", MarshalDispatch{TaskID: "a"}, process05ApprovalRequired("lane", "a", "approval")); !handled || err != nil {
		t.Fatalf("pause: %v %v", handled, err)
	}
	after, _, err := s.load(t.Context(), "serial")
	if err != nil || after.State != marshal.AwaitingUser {
		t.Fatalf("serial wait no longer pauses the run: %s %v", after.State, err)
	}
	if waiting, err := s.privateProcess05WaitPending(t.Context(), after, after.Tasks[0]); err != nil || waiting {
		t.Fatalf("serial dispatch consulted a parallel wait: %v %v", waiting, err)
	}
}

// A worker that fails in one lane of a parallel plan sends its task back for
// rework; the other lanes keep going and the run finishes.
func TestExtremeParallelFailedWorkerReworksInsteadOfStoppingRun(t *testing.T) {
	s, _, _ := parallelWaitFixture(t, 3)
	var mu sync.Mutex
	launches := map[string]int{}
	s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		id := req.Task.PlanTaskID
		mu.Lock()
		launches[id]++
		first := launches[id] == 1
		mu.Unlock()
		if id == "b" && first {
			return nil, errors.New("run failed: one or more tasks failed")
		}
		return nil, os.WriteFile(filepath.Join(req.Worktree, id+".txt"), []byte("done\n"), 0600)
	}}
	brief := func(marshal.Task, BriefContext) string { return "brief" }
	run, err := s.Execute(t.Context(), "par", brief, nil)
	for i := 0; err == nil && i < 4 && run.State != marshal.Closed; i++ {
		done := true
		for _, task := range run.Tasks {
			done = done && task.State == marshal.Merged
		}
		if done {
			break
		}
		run, err = s.Execute(t.Context(), "par", brief, nil)
	}
	if err != nil {
		t.Fatalf("one failed worker stopped the run: %v", err)
	}
	for _, task := range run.Tasks {
		if task.State != marshal.Merged {
			t.Fatalf("task %s ended %s", task.PlanTaskID, task.State)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if launches["b"] != 2 {
		t.Fatalf("failed task relaunched %d times, want once more", launches["b"]-1)
	}
}
