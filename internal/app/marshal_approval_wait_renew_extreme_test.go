//go:build !governed

package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
)

// TestExtremeParallelExpiredApprovalWaitRelaunchesOnlyThatTask: owner rule
// (Extreme): a task approval that expired unanswered never stops an isolated
// ULTRA run. Resume continues, the expired wait is cleared without charging
// rework (no worker ran), and the task's lane is relaunched to request a
// fresh approval; the other tasks are not held.
func TestExtremeParallelExpiredApprovalWaitRelaunchesOnlyThatTask(t *testing.T) {
	s, pending, mu := parallelWaitFixture(t, 3, "a")
	brief := func(marshal.Task, BriefContext) string { return "brief" }
	run, err := s.Execute(t.Context(), "par", brief, nil)
	if err != nil || run.State != marshal.AwaitingUser || run.Tasks[0].Process05WaitApprovalID != "approval-a" {
		t.Fatalf("fixture did not wait on approval-a: %+v %v", run, err)
	}
	expired := true
	receipt := s.process05WaitReceipt
	s.process05WaitReceipt = func(ctx context.Context, run marshal.Run, task marshal.Task, runID, approvalID string) (bool, error) {
		mu.Lock()
		gone := expired && task.PlanTaskID == "a"
		mu.Unlock()
		if gone {
			return false, &process05ApprovalExpired{approvalID: approvalID, expiresAt: time.Now().Add(-time.Minute)}
		}
		return receipt(ctx, run, task, runID, approvalID)
	}
	// The relaunched lane gets its fresh approval and finishes.
	mu.Lock()
	pending["a"] = false
	mu.Unlock()
	resumed, err := s.Resume(t.Context(), "par")
	if err != nil || resumed.State != marshal.Dispatching {
		t.Fatalf("an expired approval stopped resume: %s %v", resumed.State, err)
	}
	cleared, err := s.pausePrivateProcess05Waits(t.Context(), "par")
	if err != nil || !cleared {
		t.Fatalf("expired wait not cleared: %v %v", cleared, err)
	}
	mu.Lock()
	expired = false
	mu.Unlock()
	after, _, err := s.load(t.Context(), "par")
	if err != nil {
		t.Fatal(err)
	}
	a := after.Tasks[0]
	if a.State != marshal.Queued || a.Process05WaitApprovalID != "" || a.Process05WaitRunID != "" || len(a.ReturnsByAgent) != 0 {
		t.Fatalf("expired wait: want queued, cleared and no rework: %+v", a)
	}
	final, err := s.Execute(t.Context(), "par", brief, nil)
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	for _, task := range final.Tasks {
		if task.State != marshal.Merged || len(task.ReturnsByAgent) != 0 {
			t.Fatalf("task %s ended %s (returns %v)", task.PlanTaskID, task.State, task.ReturnsByAgent)
		}
	}
	if final.Pause != nil && strings.Contains(final.Pause.Detail, "approval-a") {
		t.Fatalf("run still paused on the expired approval: %+v", final.Pause)
	}
}

// TestExtremeExpiredApprovalRelaunchesBesideRunningSiblings: an approval that
// expires unanswered while siblings are still dispatchable is cleared at the
// start of the next dispatch round, so its task relaunches beside them in its
// own lane instead of waiting until nothing else can move. No rework is
// charged and the run is not paused.
func TestExtremeExpiredApprovalRelaunchesBesideRunningSiblings(t *testing.T) {
	s, _, _ := parallelWaitFixture(t, 3, "a")
	var mu sync.Mutex
	launches := map[string]int{}
	relaunched := make(chan struct{})
	expired := false
	s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		id := req.Task.PlanTaskID
		mu.Lock()
		launches[id]++
		n := launches[id]
		mu.Unlock()
		switch {
		case id == "a" && n == 1:
			return nil, process05ApprovalRequired("lane-a", "a", "approval-a")
		case id == "a":
			close(relaunched)
		case id == "b":
			// a's approval expires once its wait is recorded, while b is
			// still finishing and c has not been dispatched yet.
			deadline := time.Now().Add(10 * time.Second)
			for {
				run, _, err := s.load(context.Background(), "par")
				if err == nil && run.Tasks[0].Process05WaitApprovalID == "approval-a" {
					break
				}
				if time.Now().After(deadline) {
					t.Error("a's approval wait was never recorded")
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			mu.Lock()
			expired = true
			mu.Unlock()
		case id == "c":
			// c runs in the round after a's wait expired; a must be
			// relaunched in that same round, not after c finishes.
			select {
			case <-relaunched:
			case <-time.After(10 * time.Second):
				t.Error("expired task a was held until its sibling c finished")
			}
		}
		return nil, os.WriteFile(filepath.Join(req.Worktree, id+".txt"), []byte("done\n"), 0600)
	}}
	s.process05WaitReceipt = func(_ context.Context, _ marshal.Run, task marshal.Task, runID, approvalID string) (bool, error) {
		if task.PlanTaskID != "a" || runID != "lane-a" || approvalID != "approval-a" {
			t.Errorf("unbound continuation for %s: %s %s", task.PlanTaskID, runID, approvalID)
		}
		mu.Lock()
		defer mu.Unlock()
		if !expired {
			return true, nil
		}
		return false, &process05ApprovalExpired{approvalID: approvalID, expiresAt: time.Now().Add(-time.Minute)}
	}
	final, err := s.Execute(t.Context(), "par", func(marshal.Task, BriefContext) string { return "brief" }, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, task := range final.Tasks {
		if task.State != marshal.Merged || len(task.ReturnsByAgent) != 0 || task.Process05WaitApprovalID != "" {
			t.Fatalf("task %s ended %s (returns %v, wait %q)", task.PlanTaskID, task.State, task.ReturnsByAgent, task.Process05WaitApprovalID)
		}
	}
	if final.Pause != nil {
		t.Fatalf("run paused on an expired approval: %+v", final.Pause)
	}
	if launches["a"] != 2 {
		t.Fatalf("task a launched %d times, want 2", launches["a"])
	}
}
