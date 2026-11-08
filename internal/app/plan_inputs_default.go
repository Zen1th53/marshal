//go:build governed

package app

import (
	"context"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
)

func (s *PlanService) applyModelPlanInputs(_ context.Context, _ *plan.ExecutionPlan, req CreatePlanRequest, _ time.Time) error {
	if len(req.Checks) != 0 || len(req.TaskProviders) != 0 {
		return fmt.Errorf("%w: private model plan inputs unavailable in this build", model.ErrInvalid)
	}
	return nil
}

func privatePlanTaskWorker(plan.ExecutionPlan, plan.Task) (string, bool, error) {
	return "", false, nil
}
