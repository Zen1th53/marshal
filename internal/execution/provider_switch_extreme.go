//go:build !governed

package execution

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/Zen1th53/marshal/internal/plan"
)

// RebindTaskLaneProviderSwitch moves one unstarted lane-parent task to another
// provider within the same approved plan version, on an operator's durable
// switch decision. The task's next lane is bound to the new provider; the
// previous attempt's binding is kept as superseded. It never runs a worker
// and grants nothing: the switched task requires a fresh task approval.
func (e *Engine) RebindTaskLaneProviderSwitch(ctx context.Context, parentID string, version int64, taskID, from, next, modelName, receipt string, authorize func(ExecutionRun, TaskExecution) error) (*ExecutionRun, error) {
	return e.rebindTaskLaneProviderSwitch(ctx, parentID, version, taskID, from, next, modelName, "", receipt, authorize, false)
}

// RebindTaskLaneApprovedProviderFailover uses the same parent binding commit
// as an operator switch, but derives the model from the sealed fallback and
// authorizes the retained failed lane before superseding it.
func (e *Engine) RebindTaskLaneApprovedProviderFailover(ctx context.Context, parentID string, version int64, taskID, from, next, base, receipt string, authorize func(ExecutionRun, TaskExecution) error) (*ExecutionRun, error) {
	return e.rebindTaskLaneProviderSwitch(ctx, parentID, version, taskID, from, next, "", base, receipt, authorize, true)
}

func (e *Engine) rebindTaskLaneProviderSwitch(ctx context.Context, parentID string, version int64, taskID, from, next, modelName, base, receipt string, authorize func(ExecutionRun, TaskExecution) error, fallback bool) (*ExecutionRun, error) {
	// The same provider is allowed only to change the model of a task
	// (Extreme's flexible worker models); a fallback always changes provider.
	if from == "" || next == "" || from == next && fallback || (!fallback && modelName == "" || fallback && base == "") || receipt == "" || authorize == nil {
		return nil, fmt.Errorf("%w: incomplete provider switch binding", ErrRunConflict)
	}
	e.operationMu.Lock()
	defer e.operationMu.Unlock()
	e.laneCommitMu.Lock()
	defer e.laneCommitMu.Unlock()
	parent, err := e.store.GetRun(ctx, parentID)
	if err != nil {
		return nil, err
	}
	if parent.Version != version || parent.ParentRunID != "" || parent.Mode != plan.ModeUltra || parent.Delivery != DeliveryPreserveBranch || parent.State == RunCancelling || parent.State.IsTerminal() && parent.State != RunDonePendingVerification {
		return nil, fmt.Errorf("%w: provider switch requires the current task lane parent", ErrRunConflict)
	}
	if _, active := e.activeRuns[parentID]; active {
		return nil, fmt.Errorf("%w: whole parent is executing", ErrInvalidStateTransition)
	}
	task, ok := parent.Tasks[taskID]
	if !ok || task.RunID != parentID || task.Attempts != 0 || task.State != TaskPending {
		return nil, fmt.Errorf("%w: provider switch task binding differs", ErrRunInvalid)
	}
	if err := e.ValidateRunGoal(ctx, parent); err != nil {
		return nil, err
	}
	if fallback {
		if task.BaseCommit != "" && task.BaseCommit != base {
			return nil, ErrRunConflict
		}
		modelName, err = e.approvedFallbackSelector(ctx, parent, task, from, next)
		if err != nil {
			return nil, err
		}
	}
	if task.AssignedHarness == next && (from != next || task.AssignedModel == modelName) {
		recorded := parent.TaskProviderSwitches[taskID]
		if recorded.To != next || recorded.Model != modelName || recorded.Receipt != receipt || task.AssignedModel != modelName {
			return nil, fmt.Errorf("%w: provider switch was not prepared by this decision", ErrRunConflict)
		}
		if err := authorize(parent, task); err != nil {
			return nil, err
		}
		return &parent, nil
	}
	if task.AssignedHarness != from {
		return nil, fmt.Errorf("%w: provider switch source differs", ErrRunConflict)
	}
	if _, err = e.GetHarness(next); err != nil {
		return nil, err
	}
	authorityRun, authorityTask := parent, task
	record := parent.TaskProviderSwitches[taskID]
	if current := parent.TaskLanes[taskID]; current != "" {
		lane, err := e.store.GetRun(ctx, current)
		if err != nil {
			return nil, err
		}
		if _, active := e.activeRuns[current]; active || lane.State == RunRunning || lane.State == RunNeedsApproval || lane.State == RunCancelling {
			return nil, fmt.Errorf("%w: the task's current attempt is %s", ErrRunBlocked, lane.State)
		}
		if fallback {
			if lane.ParentRunID != parentID || lane.LaneTaskID != taskID || lane.BaseCommit != base || lane.ParentBinding != taskLaneBinding(parent, task, base) {
				return nil, fmt.Errorf("%w: failover lane binding differs", ErrRunConflict)
			}
			authorityRun, authorityTask = lane, lane.Tasks[taskID]
			if authorityTask.AssignedHarness != from {
				return nil, ErrRunConflict
			}
		}
		if lane.ParentBinding != "" && !slices.Contains(record.PriorBindings, lane.ParentBinding) {
			record.PriorBindings = append(record.PriorBindings, lane.ParentBinding)
		}
	}
	if err := authorize(authorityRun, authorityTask); err != nil {
		return nil, err
	}
	if _, err = e.journal.Append(JournalEvent{RunID: parentID, TaskID: taskID, Actor: "MARSHAL_ENGINE", EventType: "PROVIDER_SWITCH_PREPARED", Summary: fmt.Sprintf("%s: %s -> %s (%s)", receipt, from, next, modelName)}); err != nil {
		return nil, err
	}
	record.From, record.To, record.Model, record.Receipt = from, next, modelName, receipt
	if parent.TaskProviderSwitches == nil {
		parent.TaskProviderSwitches = map[string]ProviderSwitch{}
	}
	parent.TaskProviderSwitches[taskID] = record
	task.AssignedHarness = next
	task.AssignedModel = modelName
	task.AssignedAgent = ""
	task.ApprovalID = ""
	task.ApprovalRequired = true
	task.UpdatedAt = time.Now().UTC()
	parent.Tasks[taskID] = task
	if err = e.persistLaneSnapshotLocked(ctx, &parent); err != nil {
		return nil, err
	}
	return &parent, nil
}

// ProviderSwitchAssigns reports whether a task's current provider and model
// are its lane parent's recorded operator provider switch.
func ProviderSwitchAssigns(parent ExecutionRun, task TaskExecution) bool {
	record, ok := parent.TaskProviderSwitches[task.TaskID]
	original, exists := parent.Tasks[task.TaskID]
	return ok && exists && record.To != "" && record.Model != "" && record.To == task.AssignedHarness && record.Model == task.AssignedModel && original.AssignedHarness == task.AssignedHarness && original.AssignedModel == task.AssignedModel
}
