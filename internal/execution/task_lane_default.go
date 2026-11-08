//go:build governed

package execution

import (
	"context"
	"github.com/Zen1th53/marshal/internal/plan"
)

func taskWorktreeID(_ ExecutionRun, task TaskExecution) string { return branchTaskID(task.TaskID) }
func (e *Engine) reserveTaskLaneAttempt(context.Context, ExecutionRun, TaskExecution) error {
	return nil
}
func (e *Engine) validateTaskLaneExecution(ExecutionRun) error                 { return nil }
func (e *Engine) controlTaskLanes(context.Context, ExecutionRun, string) error { return nil }

func (e *Engine) persistTaskLaneRun(context.Context, *ExecutionRun) (bool, error) { return false, nil }

func privateTaskAssignment(plan.AssignmentPlan, plan.Task) (string, bool) { return "", false }

func executionHarnessName(name string) string { return name }

func producerAssignmentAllowed(plan.Assignment) bool { return true }

func (e *Engine) settleCancelledTaskLane(ExecutionRun, TaskExecution, *Lease) {}
func (e *Engine) taskLanesExecuting(string) bool                              { return false }
