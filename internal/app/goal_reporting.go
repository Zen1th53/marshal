package app

import (
	"context"
	"errors"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/verification"
)

type GoalProgress struct {
	Goal     model.GoalContract
	Criteria []verification.CriterionProgress
}

// GoalProgress projects canonical records without updating a verification session.
func (r *Runtime) GoalProgress(ctx context.Context, sessionID string) (GoalProgress, error) {
	goal, err := r.store.GetActiveGoalContract(ctx, sessionID)
	if err != nil {
		return GoalProgress{}, err
	}
	session, err := r.store.LatestVerificationForGoal(ctx, goal.ProjectID, goal.ID)
	if err != nil && !errors.Is(err, verification.ErrNotFound) {
		return GoalProgress{}, err
	}
	current := verification.Binding{}
	if err == nil {
		current, _ = r.Verification().BindingForRun(ctx, session.Binding.RunID)
	}
	current.ProjectID = goal.ProjectID
	current.GoalID = goal.ID
	current.GoalRevision = goal.Revision
	return GoalProgress{Goal: goal, Criteria: verification.ProjectCriteria(goal.SuccessCriteria, session, current, time.Now().UTC())}, nil
}
