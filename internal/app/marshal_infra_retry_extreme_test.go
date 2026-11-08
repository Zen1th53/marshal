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
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
)

// infraFailures are the failures seen in run RUN-1793898762273028817: none
// of them rejects delivered work.
var infraFailures = []string{
	"run failed: one or more tasks failed: OpenCode execution did not complete successfully",
	"codex: error: Exceeded skills context budget",
	"process 05: failed task was not limited by its provider",
	"stream: unexpected end of JSON input (malformed tool output)",
}

func failingLaneDriver(t *testing.T, failures func(id string, launch int) error) (driver.Driver, func(string) int) {
	t.Helper()
	var mu sync.Mutex
	launches := map[string]int{}
	d := driver.Governed{Provider: "test", Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		id := req.Task.PlanTaskID
		mu.Lock()
		launches[id]++
		n := launches[id]
		mu.Unlock()
		if err := failures(id, n); err != nil {
			return nil, err
		}
		return nil, os.WriteFile(filepath.Join(req.Worktree, id+".txt"), []byte("done\n"), 0600)
	}}
	return d, func(id string) int { mu.Lock(); defer mu.Unlock(); return launches[id] }
}

func executeUntil(t *testing.T, s *MarshalService, runID string, done func(marshal.Run) bool) marshal.Run {
	t.Helper()
	brief := func(marshal.Task, BriefContext) string { return "brief" }
	// Execute errors are expected while attempts fail; the stored run decides.
	run, _ := s.Execute(t.Context(), runID, brief, nil)
	for i := 0; i < 20 && !done(run); i++ {
		run, _ = s.Execute(t.Context(), runID, brief, nil)
	}
	run, _, loadErr := s.load(t.Context(), runID)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	return run
}

// Bug #23: worker failures that deliver nothing are retried outside the
// rework limit. More of them than the limit (2) still lets the task finish
// with its first worker and no rework spent.
func TestExtremeInfraFailuresDoNotSpendRework(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)
	s, _, _ := parallelWaitFixture(t, 2)
	d, launches := failingLaneDriver(t, func(id string, n int) error {
		if id == "b" && n <= len(infraFailures) {
			return errors.New(infraFailures[n-1])
		}
		return nil
	})
	s.Drivers["worker"] = d
	run := executeUntil(t, s, "par", func(run marshal.Run) bool {
		return run.Pause != nil || run.Tasks[taskIndex(run, "b")].State == marshal.Merged
	})
	b := run.Tasks[taskIndex(run, "b")]
	if b.State != marshal.Merged {
		t.Fatalf("task ended %s (%q), pause %+v", b.State, b.EscalationReason, run.Pause)
	}
	if b.Worker != "worker" {
		t.Fatalf("infra failures reassigned the task to %s", b.Worker)
	}
	if n := taskReturns(b) - b.LaneReworkBase; n != 0 {
		t.Fatalf("infra failures spent %d rework (%v)", n, b.ReturnsByAgent)
	}
	if b.InfraRetries != len(infraFailures) || launches("b") != len(infraFailures)+1 {
		t.Fatalf("infra retries %d, launches %d", b.InfraRetries, launches("b"))
	}
	decisions, err := s.Store.MarshalDecisions(t.Context(), "par")
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, event := range decisions {
		if event.TaskID == "b" && event.Type == events.EventTypeMarshalTaskReturned && event.Data["counted"] == false {
			kinds[event.Data["infra_failure"].(string)] = true
		}
	}
	for _, want := range []string{"worker process failure", "harness context or tool output", "provider cooldown or failover refusal"} {
		if !kinds[want] {
			t.Errorf("no uncounted return classified %q: %v", want, kinds)
		}
	}
}

// The infra retry budget is bounded: a provider that never works escalates
// the task with the precise last failure, and the operator can retry it even
// when that failure mentions a budget.
func TestExtremeInfraRetryLimitEscalatesWithReason(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)
	s, _, _ := parallelWaitFixture(t, 2)
	var mu sync.Mutex
	broken := true
	d, launches := failingLaneDriver(t, func(id string, _ int) error {
		mu.Lock()
		defer mu.Unlock()
		if id == "b" && broken {
			return errors.New("codex: error: Exceeded skills context budget")
		}
		return nil
	})
	s.Drivers["worker"] = d
	run := executeUntil(t, s, "par", func(run marshal.Run) bool { return run.Pause != nil })
	b := run.Tasks[taskIndex(run, "b")]
	if b.State != marshal.Escalated || !strings.HasPrefix(b.EscalationReason, infraRetryEscalation) || !strings.Contains(b.EscalationReason, "Exceeded skills context budget") {
		t.Fatalf("task ended %s (%q)", b.State, b.EscalationReason)
	}
	if launches("b") != infraRetryLimit+1 {
		t.Fatalf("launched %d times, want %d", launches("b"), infraRetryLimit+1)
	}
	if n := taskReturns(b) - b.LaneReworkBase; n != 0 {
		t.Fatalf("infra failures spent %d rework", n)
	}
	if run.Pause == nil || !strings.Contains(run.Pause.Reason, infraRetryEscalation) {
		t.Fatalf("run not paused for the exhausted infra retries: %+v", run.Pause)
	}
	mu.Lock()
	broken = false
	mu.Unlock()
	if err := s.RetryTask(t.Context(), "par", "b", true); err != nil {
		t.Fatalf("exhausted infra retries cannot be retried: %v", err)
	}
	run, _, _ = s.load(t.Context(), "par")
	if got := run.Tasks[taskIndex(run, "b")]; got.InfraRetries != 0 || got.InfraStreak != 0 {
		t.Fatalf("retry kept the infra budget: %d/%d", got.InfraRetries, got.InfraStreak)
	}
	run = executeUntil(t, s, "par", func(run marshal.Run) bool { return run.Tasks[taskIndex(run, "b")].State == marshal.Merged })
	if b := run.Tasks[taskIndex(run, "b")]; b.State != marshal.Merged {
		t.Fatalf("retried task ended %s", b.State)
	}
}

// A provider that fails the same task twice in a row cools down briefly, so
// the next attempt fails over through the cooldown admission path.
func TestExtremeInfraStreakCoolsProviderForFailover(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)
	s, _, _ := parallelWaitFixture(t, 1)
	d, _ := failingLaneDriver(t, func(string, int) error { return errors.New("dial tcp: connection refused") })
	s.Drivers["worker"] = d
	s.Drivers["other"] = d
	run, rev, err := s.load(t.Context(), "par")
	if err != nil {
		t.Fatal(err)
	}
	i := 0
	run.Tasks[i].State = marshal.Dispatched
	if err = s.returnInfraFailure(t.Context(), "par", run, rev, i, "worker failed: dial tcp: connection refused"); err != nil {
		t.Fatal(err)
	}
	if isProviderUnavailable("worker", s.clock()) {
		t.Fatal("one failure cooled the provider")
	}
	run, rev, _ = s.load(t.Context(), "par")
	run.Tasks[i].State = marshal.Dispatched
	if err = s.returnInfraFailure(t.Context(), "par", run, rev, i, "worker failed: dial tcp: connection refused"); err != nil {
		t.Fatal(err)
	}
	if !isProviderUnavailable("worker", s.clock()) {
		t.Fatal("a failure streak did not cool the provider for failover")
	}
	run, _, _ = s.load(t.Context(), "par")
	task := run.Tasks[i]
	if task.State != marshal.Returned || task.InfraRetries != 2 || taskReturns(task)-task.LaneReworkBase != 0 {
		t.Fatalf("task %s retries %d returns %v", task.State, task.InfraRetries, task.ReturnsByAgent)
	}
}

// A cooldown admission receipt justifies switching a task whose lane failed
// without naming a limit; without one the switch is still refused. Run
// RUN-1793898762273028817 returned the task twice on this refusal.
func TestFailedTaskFailoverAcceptsCooldownAdmission(t *testing.T) {
	current := execution.TaskExecution{TaskID: "t", State: execution.TaskFailed, LastFailureReason: "OpenCode execution did not complete successfully"}
	canonical := execution.ExecutionRun{Failures: []execution.RunFailure{{TaskID: "t", Stage: "EXECUTE", Reason: current.LastFailureReason}}}
	if err := failedTaskFailoverJustified(canonical, current, events.Event{Data: map[string]any{"failover": true, "admission": true}}); err != nil {
		t.Fatalf("cooldown admission refused: %v", err)
	}
	if err := failedTaskFailoverJustified(canonical, current, events.Event{Data: map[string]any{"failover": true}}); err == nil {
		t.Fatal("an unlimited failure without admission switched providers")
	}
	if err := failedTaskFailoverJustified(execution.ExecutionRun{}, current, events.Event{Data: map[string]any{"admission": true}}); err == nil {
		t.Fatal("a failover without a recorded execution failure was admitted")
	}
	limited := current
	limited.LastFailureReason = "Claude: You've hit your limit · resets 3pm"
	canonical.Failures[0].Reason = limited.LastFailureReason
	if err := failedTaskFailoverJustified(canonical, limited, events.Event{Data: map[string]any{"failover": true}}); err != nil {
		t.Fatalf("Claude 5-hour limit is not a provider limit: %v", err)
	}
}

// After a cooldown-admission failover the new worker gets the normal rework
// allowance: its first genuine review rejection spends one rework and the
// task goes back to it, instead of escalating as if a second worker had
// failed the task too.
func TestExtremeAdmissionFailoverKeepsReworkAllowance(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)
	s, _, _ := parallelWaitFixture(t, 1)
	d, launches := failingLaneDriver(t, func(string, int) error { return nil })
	s.Drivers = map[string]driver.Driver{"codex": d, "claude": d}
	var mu sync.Mutex
	reviews := 0
	accept := s.CrossReview
	s.CrossReview = func(ctx context.Context, task marshal.Task, h marshal.HandIn, c marshal.Control) (marshal.Review, string, error) {
		mu.Lock()
		reviews++
		first := reviews == 1
		mu.Unlock()
		review, provider, err := accept(ctx, task, h, c)
		if first {
			review.Verdict = marshal.VerdictReturn
			review.Reasons = []string{"the change misses a criterion"}
		}
		return review, provider, err
	}
	run, rev, err := s.load(t.Context(), "par")
	if err != nil {
		t.Fatal(err)
	}
	i := 0
	id := run.Tasks[i].PlanTaskID
	// codex already had one genuine return before it cooled down.
	run.Tasks[i].Worker = "codex"
	run.Tasks[i].ReturnsByAgent = map[string]int{"codex": 1}
	if err = s.save(t.Context(), "par", run, rev); err != nil {
		t.Fatal(err)
	}
	markProviderUnavailable("codex", s.clock().Add(time.Hour))
	run = executeUntil(t, s, "par", func(run marshal.Run) bool {
		return run.Pause != nil || run.Tasks[i].State == marshal.Merged
	})
	task := run.Tasks[i]
	if task.State != marshal.Merged || task.Worker != "claude" {
		t.Fatalf("task ended %s on %s (%q), pause %+v", task.State, task.Worker, task.EscalationReason, run.Pause)
	}
	if task.ReturnsByAgent["claude"] != 1 || len(task.ReturnsByAgent) != 1 {
		t.Fatalf("returns after failover %v, want claude 1/%d", task.ReturnsByAgent, run.Settings.ReworkLimit)
	}
	if launches(id) != 2 {
		t.Fatalf("launched %d times, want 2 (rework on the new worker)", launches(id))
	}
	decisions, err := s.Store.MarshalDecisions(t.Context(), "par")
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range decisions {
		if event.TaskID == id && event.Type == events.EventTypeMarshalEscalated {
			t.Fatalf("failover worker's first return escalated: %v", event.Data)
		}
	}
}
