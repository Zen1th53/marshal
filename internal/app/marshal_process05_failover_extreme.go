//go:build !governed

package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
)

func (r *Runtime) prepareProcess05ProviderFailover(ctx context.Context, s *MarshalService, run marshal.Run, req driver.Request, p05 *execution.ExecutionRun) (*execution.ExecutionRun, error) {
	task, ok := p05.Tasks[req.Task.PlanTaskID]
	if !ok {
		return p05, nil
	}
	if !req.Task.ProviderFailoverPending {
		return p05, nil
	}
	decisions, err := s.marshalDecisionsResolved(ctx, req.RunID)
	if err != nil {
		return nil, err
	}
	var receipt events.Event
	for i := len(decisions) - 1; i >= 0; i-- {
		event := decisions[i]
		if event.TaskID != req.Task.PlanTaskID || event.Type != events.EventTypeMarshalTaskReassigned {
			continue
		}
		receipt = event
		break
	}
	if receipt.ID == "" || receipt.Data["failover"] != true || receipt.Data["worker"] != req.Task.Worker {
		return nil, errors.New("process 05: provider failover has no matching durable decision")
	}
	from, _ := receipt.Data["previous_worker"].(string)
	// A plan without a sealed fallback for this provider continues with the
	// next provider's current verified harness profile (Extreme never stops).
	if modelName, profileErr := r.verifiedFallbackProfileModel(ctx, s, req.Task.Worker); profileErr == nil {
		// A limited default model gives way to another verified model of
		// the same provider (flexible worker models).
		if chosen, usable := flexibleWorkerModel(ctx, s.Store, req.Task.Worker, modelName, s.clock()); usable {
			modelName = chosen
		}
		ctx = execution.WithVerifiedFallbackModel(ctx, req.Task.Worker, modelName)
	}
	authorize := func(canonical execution.ExecutionRun, current execution.TaskExecution) error {
		if err := r.validateApprovedFallbackProfile(ctx, s, canonical, current, req.Task.Worker); err != nil {
			return err
		}
		if canonical.PlanID != run.PlanID || canonical.PlanVersion != run.PlanVersion || canonical.ProjectID != s.CanonicalPlanProjectID() || canonical.GoalID == "" || current.TaskID != req.Task.PlanTaskID || current.CanonicalTaskID == "" || !sameStrings(current.TargetFiles, req.Task.Files) || !sameStrings(current.Dependencies, req.Task.DependsOn) {
			return errors.New("process 05: provider failover changed canonical plan or task scope")
		}
		allowed := false
		for _, provider := range GetEffectiveFailoverOrder(run.Settings) {
			if marshalHarnessName(provider) == marshalHarnessName(req.Task.Worker) {
				allowed = true
			}
		}
		if !allowed {
			return errors.New("process 05: provider failover is outside the approved order")
		}
		if current.State == execution.TaskFailed {
			if err := failedTaskFailoverJustified(canonical, current, receipt); err != nil {
				return err
			}
		} else if receipt.Data["admission"] != true && current.AssignedHarness != req.Task.Worker {
			return errors.New("process 05: unstarted task has no cooldown admission decision")
		}
		return nil
	}
	if p05.ParentRunID == "" && run.Process05Parallel && marshalHarnessName(from) == marshalHarnessName(req.Task.Worker) {
		// A model failover within the same provider moves the task to another
		// verified model of that provider; no provider change is involved.
		modelName, usable := flexibleWorkerModel(ctx, r.store, req.Task.Worker, "", s.clock())
		if !usable {
			return nil, fmt.Errorf("process 05: %s has no other verified model to fail over to", req.Task.Worker)
		}
		if task.AssignedHarness == req.Task.Worker && task.AssignedModel == modelName {
			return p05, nil
		}
		return r.Execution().Engine().RebindTaskLaneProviderSwitch(ctx, p05.RunID, p05.Version, task.TaskID, task.AssignedHarness, req.Task.Worker, modelName, receipt.ID, authorize)
	}
	if p05.ParentRunID == "" && run.Process05Parallel {
		return r.Execution().Engine().RebindTaskLaneApprovedProviderFailover(ctx, p05.RunID, p05.Version, task.TaskID, from, req.Task.Worker, req.Task.BaseCommit, receipt.ID, authorize)
	}
	// The parent was rebound before this fresh lane opened. Revalidate the
	// same receipt and scope without treating the child as a conventional run.
	if p05.ParentRunID != "" {
		parent, err := r.Execution().Engine().GetRun(ctx, p05.ParentRunID)
		if err != nil {
			return nil, err
		}
		record := parent.TaskProviderSwitches[task.TaskID]
		if record.From != from || record.To != req.Task.Worker || record.Receipt != receipt.ID || !execution.ProviderSwitchAssigns(parent, task) {
			return nil, errors.New("process 05: provider failover lane has no matching parent binding")
		}
		if err := r.Execution().Engine().ValidateTaskLaneBinding(ctx, p05.RunID); err != nil {
			return nil, err
		}
		if err := authorize(*p05, task); err != nil {
			return nil, err
		}
		return p05, nil
	}
	return r.Execution().Engine().RebindApprovedProviderFailover(ctx, p05.RunID, p05.Version, task.TaskID, from, req.Task.Worker, req.Task.BaseCommit, receipt.ID, authorize)
}

// failedTaskFailoverJustified admits a provider switch for a task whose lane
// failed only with a durable provider reason: the failure recorded for the
// task names its provider's limit, or the failover receipt records that the
// provider was cooling down when the task was admitted again (a limit seen
// elsewhere, or a provider that failed the task repeatedly). A refusal here
// used to return the task as rework on every attempt (bug #23).
func failedTaskFailoverJustified(canonical execution.ExecutionRun, current execution.TaskExecution, receipt events.Event) error {
	failure := false
	for _, entry := range canonical.Failures {
		if entry.TaskID == current.TaskID && entry.Stage == "EXECUTE" && entry.Reason == current.LastFailureReason {
			failure = true
		}
	}
	if !failure {
		return errors.New("process 05: failed task has no provider execution failure")
	}
	limited, _, _ := classifyProviderLimit(errors.New(current.LastFailureReason), marshal.HandIn{}, nil)
	if !limited && receipt.Data["admission"] != true {
		return errors.New("process 05: failed task was not limited by its provider")
	}
	return nil
}

func (r *Runtime) process05ProviderFailure(ctx context.Context, req driver.Request, completed *execution.ExecutionRun, err error) error {
	if err == nil || completed == nil {
		return err
	}
	task, ok := completed.Tasks[req.Task.PlanTaskID]
	if !ok || task.State != execution.TaskFailed {
		return err
	}
	providerFailure := false
	for _, entry := range completed.Failures {
		if entry.TaskID == task.TaskID && entry.Stage == "EXECUTE" && entry.Reason == task.LastFailureReason {
			providerFailure = true
		}
	}
	if !providerFailure {
		return err
	}
	limited, _, _ := classifyProviderLimit(errors.New(task.LastFailureReason), marshal.HandIn{}, nil)
	if !limited {
		// Carry the task's own failure, so the returned attempt names what
		// failed instead of only "one or more tasks failed".
		if reason := strings.TrimSpace(task.LastFailureReason); reason != "" && !strings.Contains(err.Error(), reason) {
			return fmt.Errorf("%w: %s", err, reason)
		}
		return err
	}
	r.honeypotMu.Lock()
	trap := r.honeypots[task.WorktreePath]
	r.honeypotMu.Unlock()
	if trap == nil {
		return errors.New("honeypot: required scan identity unavailable after provider failure")
	}
	{
		if scanErr := r.checkHoneypot(ctx, req.Task.PlanTaskID, trap, nil, nil, EgressAlert{RunID: completed.RunID, TaskID: task.CanonicalTaskID, Worker: req.Task.Worker}); scanErr != nil {
			return scanErr
		}
	}
	return fmt.Errorf("process 05: provider execution failed: %s: %w", strings.TrimSpace(task.LastFailureReason), err)
}
