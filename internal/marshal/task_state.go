package marshal

import "fmt"

// SetTaskState applies a lifecycle move. Re-saving a known state is a no-op;
// a task without a state can only enter the queue.
func SetTaskState(task *Task, next TaskState) error {
	return setTaskState(task, next, "")
}

// RestoreAmendedTaskState retains an existing task's saved progress when a
// scoped plan amendment replaces its definition. It cannot restore a different
// task or overwrite work that has already left the queue.
func RestoreAmendedTaskState(task *Task, previous Task) error {
	if task == nil || task.PlanTaskID == "" || task.PlanTaskID != previous.PlanTaskID || task.State != Queued {
		return fmt.Errorf("cannot restore amended task: expected the same queued task")
	}
	return setTaskState(task, previous.State, "restore")
}

// ResetTaskForAmendment starts a replacement artifact at the queue. Callers
// must have accepted a new plan version; terminal work is otherwise final.
func ResetTaskForAmendment(task *Task) error {
	if task == nil {
		return fmt.Errorf("cannot reset amended task: task is missing")
	}
	if task.State != "" {
		if _, known := taskMoves[task.State]; !known {
			return fmt.Errorf("cannot reset amended task: unknown state %q", task.State)
		}
	}
	return setTaskState(task, Queued, "reset")
}

func setTaskState(task *Task, next TaskState, recovery string) error {
	if task == nil {
		return fmt.Errorf("cannot change task state: task is missing")
	}
	if _, known := taskMoves[next]; !known {
		return fmt.Errorf("cannot change task %s state: unknown state %q", task.PlanTaskID, next)
	}
	if task.State == next {
		return nil
	}
	if err := TransitionTask(task.State, next); err != nil {
		// Initialization and plan replacement copy saved progress. Each
		// exception has a restricted source and destination, and its public
		// entry point checks the task identity or replacement operation.
		_, knownSource := taskMoves[task.State]
		initial := task.State == "" && next == Queued
		restore := recovery == "restore" && task.State == Queued
		reset := recovery == "reset" && knownSource && next == Queued
		if !initial && !restore && !reset {
			return fmt.Errorf("task %s: %w; reload the task before retrying", task.PlanTaskID, err)
		}
	}
	task.State = next
	return nil
}
