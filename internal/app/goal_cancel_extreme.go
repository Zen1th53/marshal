//go:build !governed

package app

import (
	"context"

	"github.com/Zen1th53/marshal/internal/model"
)

// CommandCancelPendingGoal records refusal of the exact pending intake through
// the existing authenticated goal-edit transaction. Confirmed goals and active
// execution are controlled by their own lifecycle, rather than being cancelled
// by rewriting an old goal binding underneath a running provider.
func (r *Runtime) CommandCancelPendingGoal(ctx context.Context, envelope CommandEnvelope) (model.GoalContract, error) {
	return r.commandGoal(ctx, envelope, "goal.edit", struct{ Operation string }{"cancel-pending-goal"}, func(ctx context.Context, _ string) (model.GoalContract, error) {
		return r.RejectGoal(ctx, envelope.SessionID, envelope.ExpectedVersion, "operator cancelled pending intake")
	})
}
