//go:build governed

package app

import (
	"context"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/risk"
)

func (r *Runtime) selectProcess05TaskLane(_ context.Context, _ *MarshalService, _ marshal.Run, _ driver.Request, parent *execution.ExecutionRun) (*execution.ExecutionRun, error) {
	return parent, nil
}

func bindPrivateProcess05Tier(*marshal.Run, plan.ExecutionPlan) {}

// process05RiskAssessmentID names a provider turn's risk assessment for its
// canonical task.
func process05RiskAssessmentID(provider, canonicalTaskID string, _ execution.TaskExecution) risk.AssessmentID {
	return risk.AssessmentID("process05-" + provider + "-" + canonicalTaskID)
}
