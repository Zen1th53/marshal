package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
)

func TestBudgetLimitsAreAGoalRevisionAndBlockTheRun(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	binding, _ := projectid.LoadBinding(filepath.Join(repo.Path(), projectid.StateDirName))
	store, err := execution.NewFileRunStore(filepath.Join(repo.Path(), ".marshal", "execution", "runs"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	// The run has already used two model calls under revision 2 of the goal.
	if err := store.CreateRun(ctx, execution.ExecutionRun{RunID: "RUN-budget", ProjectID: binding.ID, SessionID: "S", GoalID: "GOAL-b", GoalRevision: 2,
		State: execution.RunRunning, BudgetConsumed: execution.BudgetUsage{ModelCalls: 2},
		Tasks: map[string]execution.TaskExecution{"T": {TaskID: "T", State: execution.TaskPending}}, StartedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	r, err := Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	goal := model.GoalContract{ID: "GOAL-b", SessionID: "S", ProjectID: r.ProjectIdentity(), Revision: 1, OriginalRequest: "x", DesiredOutcome: "x",
		ConstitutionVersion: constitution.Current.String(), Confirmation: model.ConfirmationApproved, Risk: model.R1, AuthoritySource: "owner"}
	if err := r.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	local, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	owner := local.Context(ctx)
	env := CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "S", TargetID: "GOAL-b", ExpectedVersion: 1, IdempotencyKey: "b1"}

	if _, err := r.CommandSetGoalBudget(owner, env, model.BudgetLimit{MaxTotalTokens: new(int64)}); !errors.Is(err, model.ErrGoalInvalid) {
		t.Fatalf("token limit accepted: %v", err)
	}
	revised, err := r.CommandSetGoalBudget(owner, env, model.BudgetLimit{MaxModelCalls: 2})
	if err != nil || revised.Revision != 2 || revised.Confirmation != model.ConfirmationPending || revised.BudgetLimits == nil || revised.BudgetLimits.MaxModelCalls != 2 {
		t.Fatalf("budget revision: %+v %v", revised, err)
	}
	stored, err := r.Store().GetGoalContract(ctx, "GOAL-b", 2)
	if err != nil || stored.BudgetLimits == nil || stored.BudgetLimits.MaxModelCalls != 2 {
		t.Fatalf("limits not durable: %+v %v", stored, err)
	}

	executed, err := r.Execution().ExecuteRun(ctx, "RUN-budget")
	if err != nil {
		t.Fatal(err)
	}
	if executed.State != execution.RunBlocked || len(executed.Failures) == 0 || !strings.Contains(executed.Failures[len(executed.Failures)-1].Reason, string(model.ReasonBudgetExhaustedCalls)) {
		t.Fatalf("run not blocked by its budget: state=%s failures=%+v", executed.State, executed.Failures)
	}

	cleared, err := r.CommandSetGoalBudget(owner, CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "S", TargetID: "GOAL-b", ExpectedVersion: 2, IdempotencyKey: "b2"}, model.BudgetLimit{})
	if err != nil || cleared.BudgetLimits != nil || cleared.Revision != 3 {
		t.Fatalf("clear: %+v %v", cleared, err)
	}
}
