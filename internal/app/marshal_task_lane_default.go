//go:build governed

package app

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/risk"
)

func (r *Runtime) selectProcess05TaskLane(ctx context.Context, s *MarshalService, run marshal.Run, req driver.Request, parent *execution.ExecutionRun) (*execution.ExecutionRun, error) {
	if s != nil {
		pin, err := s.admitLaunchBase(ctx, req.RunID, run, req.Task, nil)
		if err != nil {
			return nil, err
		}
		if req.Task.BaseCommit != pin.AdmittedBaseCommit {
			return nil, newStaleBaseRefusal(marshal.EffectsNone, LaunchBaseRefusalError{
				TaskID:          req.Task.PlanTaskID,
				ApprovedRef:     pin.DeliveryRef,
				ApprovedCommit:  pin.ApprovedBaseCommit,
				CurrentRef:      pin.DeliveryRef,
				CurrentCommit:   pin.DeliveryCommit,
				IntegrationRef:  pin.IntegrationRef,
				IntegrationHead: pin.IntegrationHead,
				Route:           "refresh task base before launching governed task",
				Reason:          fmt.Sprintf("stale_base: governed task base %s differs from admitted base %s", req.Task.BaseCommit, pin.AdmittedBaseCommit),
				NextCommands:    []string{"/marshal amend", "/marshal use-plan"},
			})
		}
		if parent != nil && parent.BaseCommit != "" && parent.BaseCommit != run.BaseCommit {
			return nil, newStaleBaseRefusal(marshal.EffectsNone, LaunchBaseRefusalError{
				TaskID:         req.Task.PlanTaskID,
				ApprovedRef:    pin.DeliveryRef,
				ApprovedCommit: pin.ApprovedBaseCommit,
				CurrentRef:     pin.DeliveryRef,
				CurrentCommit:  parent.BaseCommit,
				Route:          "review and approve a plan on the current delivery base",
				Reason:         "stale_base: execution parent base differs from approved delivery base",
				NextCommands:   []string{"/marshal amend", "/marshal use-plan"},
			})
		}
	}
	return parent, nil
}

// closeOrphanProcess05Lane has nothing to close: this edition runs no task lanes.
func (r *Runtime) closeOrphanProcess05Lane(context.Context, marshal.Run, marshal.Task) error {
	return execution.ErrRunInvalid
}

func retireInterruptedLane(*marshal.Task) {}

func bindPrivateProcess05Tier(*marshal.Run, plan.ExecutionPlan) {}

// continuePriorTaskLanes has nothing to number: this edition runs no task lanes.
func continuePriorTaskLanes(context.Context, *MarshalService, *marshal.Run, model.GoalContract, projectid.ID) error {
	return nil
}

// process05RiskAssessmentID names a provider turn's risk assessment for its
// canonical task.
func process05RiskAssessmentID(provider, canonicalTaskID string, _ execution.TaskExecution) risk.AssessmentID {
	return risk.AssessmentID("process05-" + provider + "-" + canonicalTaskID)
}
