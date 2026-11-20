//go:build !governed

package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
)

// A dispatch refused while opening its task lane (no worker ran) is not a
// return: a retry-safe refusal is retried once by itself, then the task
// escalates with the refusal, spends no rework and is not relaunched again
// until the operator retries it. Bug #28: an orphan lane left
// RUNNING by a restart made every attempt refuse in seconds and burned the
// whole rework limit.
func TestExtremeLaneAdmissionRefusalEscalatesWithoutSpendingRework(t *testing.T) {
	s, _, _ := parallelWaitFixture(t, 2)
	var mu sync.Mutex
	launches := map[string]int{}
	refuse := true
	s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		id := req.Task.PlanTaskID
		mu.Lock()
		launches[id]++
		blocked := id == "b" && refuse
		mu.Unlock()
		if blocked {
			return nil, fmt.Errorf("worker failed: %w: previous task lane attempt is RUNNING", execution.ErrRunBlocked)
		}
		return nil, os.WriteFile(filepath.Join(req.Worktree, id+".txt"), []byte("done\n"), 0600)
	}}
	brief := func(marshal.Task, BriefContext) string { return "brief" }
	run, _ := s.Execute(t.Context(), "par", brief, nil)
	for i := 0; i < 4 && run.Pause == nil; i++ {
		run, _ = s.Execute(t.Context(), "par", brief, nil)
	}
	run, _, err := s.load(t.Context(), "par")
	if err != nil {
		t.Fatal(err)
	}
	b := run.Tasks[taskIndex(run, "b")]
	if b.State != marshal.Escalated || !strings.HasPrefix(b.EscalationReason, laneAdmissionEscalation) {
		t.Fatalf("refused task ended %s (%q), want escalated with the refusal", b.State, b.EscalationReason)
	}
	if n := taskReturns(b) - b.LaneReworkBase; n != 0 {
		t.Fatalf("a lane refusal spent %d rework", n)
	}
	// The sibling lane may still be merging; the pause holds the run either way.
	if run.Pause == nil || run.Pause.Reason != "A task couldn't start: its workspace didn't match the approved plan. Nothing ran." || !strings.HasPrefix(run.Pause.Detail, laneAdmissionEscalation+": ") {
		t.Fatalf("run was not paused for the refusal: %s %+v", run.State, run.Pause)
	}
	mu.Lock()
	if launches["b"] != 2 {
		mu.Unlock()
		t.Fatalf("refused task was launched %d times, want 2 (one retry by itself)", launches["b"])
	}
	refuse = false
	mu.Unlock()
	if err = s.RetryTask(t.Context(), "par", "b", true); err != nil {
		t.Fatalf("an escalated lane refusal cannot be retried: %v", err)
	}
	run, err = s.Execute(t.Context(), "par", brief, nil)
	for i := 0; err == nil && i < 4; i++ {
		if task := run.Tasks[taskIndex(run, "b")]; task.State == marshal.Merged {
			break
		}
		run, err = s.Execute(t.Context(), "par", brief, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	if task := run.Tasks[taskIndex(run, "b")]; task.State != marshal.Merged {
		t.Fatalf("retried task ended %s", task.State)
	}
}

func TestTaskLaneAdmissionRefusalMatchesOnlyLaneRefusals(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{fmt.Errorf("%w: task lane rework is out of order", execution.ErrRunConflict), true},
		{fmt.Errorf("x: %w", fmt.Errorf("%w: previous task lane attempt is RUNNING", execution.ErrRunBlocked)), true},
		{fmt.Errorf("worker failed: execution run blocked: orphan task lane requires explicit reconciliation"), true},
		{fmt.Errorf("%w: parent model-call budget exhausted", execution.ErrRunBlocked), false},
		{fmt.Errorf("run failed: one or more tasks failed"), false},
		{nil, false},
	} {
		if got := isTaskLaneAdmissionRefusal(tc.err); got != tc.want {
			t.Errorf("%v: got %v want %v", tc.err, got, tc.want)
		}
	}
}

// A lane refused because a restart interrupted the task's earlier attempt is
// not a workspace mismatch. When Marshal closes that attempt the task starts
// again in its next lane at once, uncounted, and Activity says why; when it
// cannot close it the pause says the attempt was interrupted, with the raw
// refusal kept in the detail.
func TestExtremeOrphanLaneRefusalSaysTheAttemptWasInterrupted(t *testing.T) {
	orphanErr := fmt.Errorf("worker failed: %w: orphan task lane requires explicit reconciliation", execution.ErrRunBlocked)
	for _, closes := range []bool{true, false} {
		t.Run(fmt.Sprint("closes=", closes), func(t *testing.T) {
			s, _, _ := parallelWaitFixture(t, 2)
			var mu sync.Mutex
			launches := map[string]int{}
			bases := []int{}
			var activity []string
			s.RecordActivity = func(line string) { mu.Lock(); activity = append(activity, line); mu.Unlock() }
			s.process05CloseOrphanLane = func(_ context.Context, _ marshal.Run, task marshal.Task) error {
				mu.Lock()
				defer mu.Unlock()
				bases = append(bases, task.LaneReworkBase)
				if !closes {
					return fmt.Errorf("%w: task lane is still executing", execution.ErrRunBlocked)
				}
				return nil
			}
			s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
				id := req.Task.PlanTaskID
				mu.Lock()
				launches[id]++
				first := launches[id] == 1
				mu.Unlock()
				if id == "b" && first {
					return nil, orphanErr
				}
				return nil, os.WriteFile(filepath.Join(req.Worktree, id+".txt"), []byte("done\n"), 0600)
			}}
			brief := func(marshal.Task, BriefContext) string { return "brief" }
			var run marshal.Run
			for i := 0; i < 6; i++ {
				run, _ = s.Execute(t.Context(), "par", brief, nil)
				if run.Pause != nil || run.Tasks[taskIndex(run, "b")].State == marshal.Merged {
					break
				}
			}
			run, _, err := s.load(t.Context(), "par")
			if err != nil {
				t.Fatal(err)
			}
			b := run.Tasks[taskIndex(run, "b")]
			mu.Lock()
			defer mu.Unlock()
			if len(bases) != 1 || bases[0] != 0 {
				t.Fatalf("Marshal tried to close the interrupted attempt %v times", bases)
			}
			if closes {
				if run.Pause != nil || b.State != marshal.Merged || launches["b"] != 2 || b.LaneReworkBase != 1 || taskReturns(b)-b.LaneReworkBase != 0 {
					t.Fatalf("closed attempt did not restart the task: %s %+v launches=%d base=%d", b.State, run.Pause, launches["b"], b.LaneReworkBase)
				}
				want := "b: its earlier attempt was interrupted; Marshal closed it and will start the task again."
				if title := strings.TrimSpace(b.Title); title != "" {
					want = title + ": its earlier attempt was interrupted; Marshal closed it and will start the task again."
				}
				if !strings.Contains(strings.Join(activity, "\n"), want) {
					t.Fatalf("activity %q lacks %q", activity, want)
				}
				return
			}
			if b.State != marshal.Escalated || launches["b"] != 1 {
				t.Fatalf("unclosed attempt: %s launches=%d", b.State, launches["b"])
			}
			if run.Pause == nil || run.Pause.Reason != "A task's earlier attempt was interrupted, and Marshal couldn't close it. Nothing new ran." || strings.Contains(run.Pause.Reason, "workspace") {
				t.Fatalf("pause words: %+v", run.Pause)
			}
			if !strings.Contains(run.Pause.Detail, "orphan task lane requires explicit reconciliation") || !strings.Contains(run.Pause.Detail, "task lane is still executing") {
				t.Fatalf("pause detail lost the raw refusal: %q", run.Pause.Detail)
			}
		})
	}
}
