package app

import (
	"context"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
)

// ActivePlanScope names the current plan and the canonical store IDs of its
// tasks. Membership is derived from the plan with canonicalPlanTaskID, the
// same derivation dispatch uses, never guessed from display IDs.
type ActivePlanScope struct {
	PlanID      string
	PlanVersion int64
	TaskIDs     map[string]bool
}

func (r *Runtime) ActivePlanScope(ctx context.Context) (ActivePlanScope, error) {
	if r == nil || r.store == nil {
		return ActivePlanScope{}, model.ErrUnavailable
	}
	service := r.Plans()
	if service == nil {
		return ActivePlanScope{}, model.ErrUnavailable
	}
	current, err := service.Current(ctx, projectid.ID(r.ProjectIdentity()))
	if err != nil {
		return ActivePlanScope{}, err
	}
	scope := ActivePlanScope{PlanID: current.ID, PlanVersion: current.Version, TaskIDs: make(map[string]bool, len(current.Tasks))}
	for _, task := range current.Tasks {
		scope.TaskIDs[canonicalPlanTaskID(current.ID, current.Version, task.ID)] = true
	}
	return scope, nil
}
