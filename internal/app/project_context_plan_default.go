//go:build governed

package app

import (
	"context"
	"github.com/Zen1th53/marshal/internal/plan"
)

func (*PlanService) bindRequiredPlanProjectContract(context.Context, *plan.ExecutionPlan) error {
	return nil
}
