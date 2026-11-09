//go:build !governed

package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
)

// prepareProcess05ProviderSwitch rebinds a governed task to the provider an
// operator reassigned it to, before the task's next lane opens. Automatic
// failover delegates to its own receipt validation. For an operator switch,
// only the operator's latest reassignment of this exact task to this exact
// worker is authority. The new provider's model comes
// from its current verified harness profile, and the task then needs a fresh
// task approval bound to that provider.
func (r *Runtime) prepareProcess05ProviderSwitch(ctx context.Context, s *MarshalService, run marshal.Run, req driver.Request, p05 *execution.ExecutionRun) (*execution.ExecutionRun, error) {
	// Failover must change the parent before the replacement lane is opened.
	if providerFailoverPending(req.Task) && p05.ParentRunID == "" && run.Process05Parallel {
		return r.prepareProcess05ProviderFailover(ctx, s, run, req, p05)
	}
	task, ok := p05.Tasks[req.Task.PlanTaskID]
	if !ok || providerFailoverPending(req.Task) || !run.Process05Bound || req.Task.Mode != marshal.Governed {
		return p05, nil
	}
	authorize := func(canonical execution.ExecutionRun, current execution.TaskExecution) error {
		if canonical.PlanID != run.PlanID || canonical.PlanVersion != run.PlanVersion || canonical.ProjectID != s.CanonicalPlanProjectID() || canonical.GoalID == "" || current.TaskID != req.Task.PlanTaskID || current.CanonicalTaskID == "" || !sameStrings(current.TargetFiles, req.Task.Files) || !sameStrings(current.Dependencies, req.Task.DependsOn) {
			return errors.New("process 05: provider switch changed canonical plan or task scope")
		}
		return nil
	}
	now := time.Now().UTC()
	if task.AssignedHarness == req.Task.Worker {
		// Flexible worker models: the plan's model is a preference. When it
		// is not verified or is limited, the task moves to another verified
		// model of the same provider before its lane opens.
		modelName, usable := flexibleWorkerModel(ctx, r.store, req.Task.Worker, task.AssignedModel, now)
		if !usable || modelName == task.AssignedModel || p05.ParentRunID != "" || !run.Process05Parallel || task.State != execution.TaskPending || task.Attempts != 0 {
			return p05, nil
		}
		receipt, err := r.recordFlexibleWorkerChoice(ctx, s, req, task.AssignedHarness, task.AssignedModel, modelName)
		if err != nil {
			return nil, err
		}
		return r.Execution().Engine().RebindTaskLaneProviderSwitch(ctx, p05.RunID, p05.Version, task.TaskID, task.AssignedHarness, req.Task.Worker, modelName, receipt, authorize)
	}
	decisions, err := s.marshalDecisionsResolved(ctx, req.RunID)
	if err != nil {
		return nil, err
	}
	receiptID := ""
	if receipt, ok := operatorReassignment(decisions, req.Task.PlanTaskID, req.Task.Worker); ok {
		receiptID = receipt.ID
	} else if !flexibleWorkerModels {
		return p05, nil // No operator switch: the harness check refuses the dispatch.
	}
	if s.workerGovernance(ctx, req.Task.Worker).State != constitution.GovernanceVerified {
		return nil, fmt.Errorf("process 05: provider switch to %s requires a current verified harness profile", req.Task.Worker)
	}
	profile, err := r.store.GetHarnessProfile(ctx, marshalHarnessName(req.Task.Worker))
	if err != nil || profile == nil || profile.ProbeEvidenceID == "" || profile.DefaultModel == "" || !profile.IsFresh(now) {
		return nil, fmt.Errorf("process 05: provider switch to %s requires a current verified harness profile", req.Task.Worker)
	}
	modelName := profile.DefaultModel
	if chosen, usable := flexibleWorkerModel(ctx, r.store, req.Task.Worker, profile.DefaultModel, now); usable {
		modelName = chosen
	}
	if err := validatePrivateAGYModel(req.Task.Worker, modelName); err != nil {
		return nil, err
	}
	if receiptID == "" {
		// The Marshal chose this worker itself: in Extreme a worker that
		// differs from the plan is a recorded runtime decision, not a refusal.
		if receiptID, err = r.recordFlexibleWorkerChoice(ctx, s, req, task.AssignedHarness, task.AssignedModel, modelName); err != nil {
			return nil, err
		}
	}
	return r.Execution().Engine().RebindTaskLaneProviderSwitch(ctx, p05.RunID, p05.Version, task.TaskID, task.AssignedHarness, req.Task.Worker, modelName, receiptID, authorize)
}

// recordFlexibleWorkerChoice records the runtime's own choice of a task's
// worker or model and returns its id as the rebind receipt. It is not a task
// reassignment event, so it never stands in for an operator or failover one.
func (r *Runtime) recordFlexibleWorkerChoice(ctx context.Context, s *MarshalService, req driver.Request, fromWorker, fromModel, toModel string) (string, error) {
	event, err := s.decisionEvent(req.RunID, req.Task.PlanTaskID, events.EventTypePolicyAllowed, map[string]any{
		"decision": "flexible worker model", "previous_worker": fromWorker, "previous_model": fromModel,
		"worker": req.Task.Worker, "model": toModel, "decided_by": "marshal-runtime", "source": "marshal",
	})
	if err != nil {
		return "", err
	}
	if _, err := s.Store.Append(ctx, event); err != nil {
		return "", err
	}
	return event.ID, nil
}

// providerSwitchAssignment reports whether a task's provider and model are its
// lane parent's recorded operator provider switch.
func (r *Runtime) providerSwitchAssignment(ctx context.Context, run execution.ExecutionRun, task execution.TaskExecution) bool {
	parent := run
	if run.ParentRunID != "" {
		var err error
		if parent, err = r.Execution().Engine().GetRun(ctx, run.ParentRunID); err != nil {
			return false
		}
	}
	return parent.PlanID == run.PlanID && parent.PlanVersion == run.PlanVersion && execution.ProviderSwitchAssigns(parent, task)
}

// operatorReassignment returns the task's latest reassignment when the
// operator made it to exactly this worker. A later runtime reassignment of
// the task supersedes it.
func operatorReassignment(decisions []events.Event, taskID, worker string) (events.Event, bool) {
	for i := len(decisions) - 1; i >= 0; i-- {
		if decisions[i].TaskID == taskID && decisions[i].Type == events.EventTypeMarshalTaskReassigned {
			ok := decisions[i].ID != "" && decisions[i].Data["actor"] == "operator" && decisions[i].Data["worker"] == worker
			return decisions[i], ok
		}
	}
	return events.Event{}, false
}
