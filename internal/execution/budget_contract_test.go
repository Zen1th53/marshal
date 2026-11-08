package execution

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/budget"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

func budgetDispatchEngine(t *testing.T, limits *model.BudgetLimit) (*Engine, *store.Store, ExecutionRun) {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	g := model.GoalContract{ID: "g", SessionID: "s", ProjectID: string(testProjectID), Revision: 1, DesiredOutcome: "x", Risk: model.R1, AuthoritySource: "owner", Confirmation: model.ConfirmationApproved, BudgetLimits: limits}
	if err = s.SaveGoalContract(ctx, g, 0); err != nil {
		t.Fatal(err)
	}
	runs := NewMemoryRunStore()
	e, err := NewEngineWithReaders(EngineConfig{ProjectRoot: t.TempDir()}, runs, nil, nil, s)
	if err != nil {
		t.Fatal(err)
	}
	run := ExecutionRun{RunID: "run", SessionID: "s", GoalID: "g", GoalRevision: 1, ProjectID: testProjectID, State: RunRunning, Tasks: map[string]TaskExecution{}, Version: 1, StartedAt: time.Now().UTC()}
	if err = runs.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	return e, s, run
}

func TestBudgetRealDispatchPreservesVerificationReserve(t *testing.T) {
	e, s, run := budgetDispatchEngine(t, &model.BudgetLimit{MaxModelCalls: 2, VerificationReserveCalls: 1})
	launches := 0
	h := NewMockHarness("bounded-test", func(_ context.Context, t TaskExecution, _ ConstraintPackage, _ string) (TaskResult, error) {
		launches++
		return TaskResult{TaskID: t.TaskID, Success: true}, nil
	})
	first := TaskExecution{TaskID: "a", Attempts: 1}
	second := TaskExecution{TaskID: "b", Attempts: 1}
	if _, err := e.dispatchBudgetedTask(t.Context(), run, first, ConstraintPackage{}, "", h); err != nil {
		t.Fatal(err)
	}
	if _, err := e.dispatchBudgetedTask(t.Context(), run, second, ConstraintPackage{}, "", h); err == nil {
		t.Fatal("build dispatched into reserve")
	}
	if launches != 1 {
		t.Fatalf("launches=%d", launches)
	}
	if admitted, err := s.ReserveGoalCall(t.Context(), "s", "g", 1, budget.CallRequest{Key: "verify", Process: budget.ProcessVerification}); err != nil || !admitted {
		t.Fatalf("Process06 reservation %v %v", admitted, err)
	}
}

func TestCancelGoalRaceWithEditAndDispatch(t *testing.T) {
	e, s, run := budgetDispatchEngine(t, nil)
	started := make(chan struct{})
	stopped := make(chan struct{})
	finished := make(chan error, 1)
	h := NewMockHarness("wait", func(ctx context.Context, t TaskExecution, _ ConstraintPackage, _ string) (TaskResult, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return TaskResult{TaskID: t.TaskID}, ctx.Err()
	})
	go func() {
		_, err := e.dispatchBudgetedTask(context.Background(), run, TaskExecution{TaskID: "a", Attempts: 1}, ConstraintPackage{}, "", h)
		finished <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("provider did not start")
	}
	if _, err := s.CancelGoalContract(t.Context(), "s", "g", 1, "stop"); err != nil {
		t.Fatal(err)
	}
	current, err := e.GetRun(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	result, err := e.ControlRun(t.Context(), "run", "cancel", current.Version)
	if err != nil || result.Run.State != RunCancelled {
		t.Fatalf("cancel %+v %v", result, err)
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("provider context was not cancelled")
	}
	<-finished
	if _, err := e.dispatchBudgetedTask(t.Context(), run, TaskExecution{TaskID: "b", Attempts: 1}, ConstraintPackage{}, "", h); err == nil {
		t.Fatal("provider dispatched after cancellation")
	}
	if _, err := e.ControlRun(t.Context(), "run", "resume", result.Run.Version); err == nil {
		t.Fatal("cancelled goal resumed")
	}
}

func TestBudgetUnknownUsageBlocksRealDispatch(t *testing.T) {
	tokens := int64(100)
	e, _, run := budgetDispatchEngine(t, &model.BudgetLimit{MaxTotalTokens: &tokens})
	h := NewMockHarness("never", func(context.Context, TaskExecution, ConstraintPackage, string) (TaskResult, error) {
		t.Error("unknown provider launched")
		return TaskResult{}, nil
	})
	if _, err := e.dispatchBudgetedTask(t.Context(), run, TaskExecution{TaskID: "a", Attempts: 1}, ConstraintPackage{}, "", h); err == nil {
		t.Fatal("unknown token bound admitted")
	}
}

func TestBudgetDurationInterruptsActiveTurn(t *testing.T) {
	e, s, run := budgetDispatchEngine(t, &model.BudgetLimit{MaxDuration: 100 * time.Millisecond})
	h := NewMockHarness("deadline", func(ctx context.Context, t TaskExecution, _ ConstraintPackage, _ string) (TaskResult, error) {
		select {
		case <-ctx.Done():
			return TaskResult{TaskID: t.TaskID}, ctx.Err()
		case <-time.After(time.Second):
			t0 := TaskResult{}
			return t0, nil
		}
	})
	if _, err := e.dispatchBudgetedTask(t.Context(), run, TaskExecution{TaskID: "a", Attempts: 1}, ConstraintPackage{}, "", h); err == nil {
		t.Fatal("duration did not interrupt turn")
	}
	used, err := s.GetBudgetTracker(t.Context(), "s", "g", 1)
	if err != nil || used.Duration < 100*time.Millisecond {
		t.Fatalf("settlement %+v %v", used, err)
	}
}
