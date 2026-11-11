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
// return: the task escalates once with the refusal, spends no rework and is
// not relaunched until the operator retries it. Bug #28: an orphan lane left
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
	if launches["b"] != 1 {
		mu.Unlock()
		t.Fatalf("refused task was relaunched %d times without a retry", launches["b"]-1)
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
