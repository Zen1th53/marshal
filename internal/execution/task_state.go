package execution

import (
	"fmt"
	"time"
)

// AllowedTaskTransitions defines the canonical state machine transitions for tasks.
var AllowedTaskTransitions = map[TaskExecutionState][]TaskExecutionState{
	TaskPending: {
		TaskReady,
		TaskBlocked,
		TaskCancelled,
	},
	TaskReady: {
		TaskAssigned,
		TaskNeedsApproval,
		TaskBlocked,
		TaskCancelled,
		TaskPaused,
	},
	TaskAssigned: {
		TaskRunning,
		TaskNeedsApproval,
		TaskBlocked,
		TaskCancelled,
		TaskFailed,
		TaskPaused,
	},
	TaskRunning: {
		TaskWaitingTool,
		TaskWaitingAgent,
		TaskNeedsApproval,
		TaskCompletedPendingVerify,
		TaskFailed,
		TaskBlocked,
		TaskCancelled,
		TaskPaused,
	},
	TaskWaitingTool: {
		TaskRunning,
		TaskNeedsApproval,
		TaskCompletedPendingVerify,
		TaskFailed,
		TaskBlocked,
		TaskCancelled,
	},
	TaskWaitingAgent: {
		TaskRunning,
		TaskNeedsApproval,
		TaskCompletedPendingVerify,
		TaskFailed,
		TaskBlocked,
		TaskCancelled,
	},
	TaskNeedsApproval: {
		TaskRunning,
		TaskAssigned,
		TaskBlocked,
		TaskCancelled,
		TaskFailed,
	},
	TaskPaused: {
		TaskReady,
		TaskAssigned,
		TaskRunning,
		TaskCancelled,
		TaskFailed,
	},
	// Terminal states have no outgoing transitions
	TaskCompletedPendingVerify: {},
	TaskFailed:                 {},
	TaskCancelled:              {},
	TaskBlocked:                {},
}

// CanTransitionTask checks if a transition from `from` to `to` is valid.
func CanTransitionTask(from, to TaskExecutionState) bool {
	allowed, exists := AllowedTaskTransitions[from]
	if !exists {
		return false
	}
	for _, s := range allowed {
		if s == to {
			return true
		}
	}
	return false
}

// TransitionTask validates and applies a transition to the task execution.
func TransitionTask(task *TaskExecution, next TaskExecutionState, now time.Time) error {
	if task == nil {
		return fmt.Errorf("%w: nil task", ErrRunInvalid)
	}

	if !CanTransitionTask(task.State, next) {
		return fmt.Errorf("%w: invalid task state transition from %s to %s for task %s",
			ErrInvalidStateTransition, task.State, next, task.TaskID)
	}

	// Invariant: No PENDING -> COMPLETED shortcut.
	if task.State == TaskPending && next == TaskCompletedPendingVerify {
		return fmt.Errorf("%w: shortcut from PENDING to COMPLETED_PENDING_VERIFY is forbidden", ErrInvalidStateTransition)
	}

	// Invariant: Completion requires collected evidence and no unresolved approval.
	if next == TaskCompletedPendingVerify {
		if err := validateTaskCompletion(*task); err != nil {
			return fmt.Errorf("%w: task completion requirements not met: %v", ErrEvidenceMissing, err)
		}
		task.CompletedAt = &now
	}

	if (task.State == TaskReady || task.State == TaskAssigned) && next == TaskRunning {
		if task.StartedAt == nil {
			task.StartedAt = &now
		}
	}

	task.State = next
	task.UpdatedAt = now
	return nil
}

func validateTaskCompletion(task TaskExecution) error {
	// If task required approval, it cannot be completed while in approval requested
	if task.ApprovalRequired && task.ApprovalID != "" && task.State == TaskNeedsApproval {
		return fmt.Errorf("task %s has unresolved approval %s", task.TaskID, task.ApprovalID)
	}

	// Verify all required evidence has been collected
	if len(task.RequiredEvidence) > 0 {
		collectedMap := make(map[string]bool, len(task.CollectedEvidence))
		for _, e := range task.CollectedEvidence {
			collectedMap[e] = true
		}
		for _, req := range task.RequiredEvidence {
			if !collectedMap[req] {
				return fmt.Errorf("task %s requires evidence %q which has not been collected", task.TaskID, req)
			}
		}
	}

	return nil
}
