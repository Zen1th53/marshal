package app

import (
	"context"

	"github.com/Zen1th53/marshal/internal/budget"
	"github.com/Zen1th53/marshal/internal/model"
)

// CommandPatchGoalBudget preserves every other limit and cumulative usage.
// Approval and planning must bind the new PENDING revision before dispatch.
func (r *Runtime) CommandPatchGoalBudget(ctx context.Context, e CommandEnvelope, patch model.BudgetPatch, reason string) (model.GoalContract, error) {
	return r.commandGoal(ctx, e, "goal.edit", struct {
		Patch  model.BudgetPatch
		Reason string
	}{patch, reason}, func(ctx context.Context, _ string) (model.GoalContract, error) {
		return r.editGoal(ctx, e, GoalEdit{BudgetPatch: &patch, Reason: reason})
	})
}

// ReserveGoalCall is the execution reader's optional durable budget contract.
// It returns false for a replay: ambiguous or repeated intent must not launch twice.
func (r *storePlanGoalReader) ReserveGoalCall(ctx context.Context, session, id string, revision int64, request budget.CallRequest) (bool, error) {
	return r.store.ReserveGoalCall(ctx, session, id, revision, request)
}

// BeginGoalDispatch holds the SQLite writer reservation through launch admission.
// Cancellation/edit cannot commit between the canonical check and dispatch.
func (r *storePlanGoalReader) BeginGoalDispatch(ctx context.Context, session, id string, revision int64) (func(), error) {
	return r.store.BeginGoalDispatch(ctx, session, id, revision)
}

func (r *storePlanGoalReader) GoalBudgetConsumed(ctx context.Context, session, id string, revision int64) (model.ConsumedBudget, error) {
	return r.store.GoalBudgetConsumed(ctx, session, id, revision)
}

func (r *storePlanGoalReader) SettleGoalCall(ctx context.Context, session, id string, revision int64, key string, result budget.CallSettlement) error {
	return r.store.SettleGoalCall(ctx, session, id, revision, key, result)
}

func (r *storePlanGoalReader) ReleaseGoalCall(ctx context.Context, session, id string, revision int64, key string, retry bool) error {
	return r.store.ReleaseGoalCall(ctx, session, id, revision, key, retry)
}
