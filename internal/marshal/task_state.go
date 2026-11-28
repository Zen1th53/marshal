package marshal

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/Zen1th53/marshal/internal/statepath"
)

// SetTaskState applies a lifecycle move. Re-saving a known state is a no-op;
// a task without a state can only enter the queue.
func SetTaskState(task *Task, next TaskState) error {
	return setTaskState(task, next, nil, false)
}

// RestoreAmendedTaskState retains an existing task's saved progress when a
// scoped plan amendment replaces its definition. It cannot restore a different
// task or overwrite work that has already left the queue. The caller supplies
// that task's prior persisted snapshot; only its exact state can be restored,
// including a terminal state.
func RestoreAmendedTaskState(task *Task, previous Task) error {
	if task == nil || task.PlanTaskID == "" || task.PlanTaskID != previous.PlanTaskID || task.State != Queued {
		return fmt.Errorf("cannot restore amended task: expected the same queued task")
	}
	return setTaskState(task, previous.State, &previous, false)
}

// ResetTaskForAmendment starts a replacement artifact at the queue. Callers
// must have accepted a new plan version. Terminal work can only be replaced
// with a changed definition on a newer artifact branch, bound to its saved snapshot.
func ResetTaskForAmendment(task *Task, previous ...Task) error {
	if task == nil {
		return fmt.Errorf("cannot reset amended task: task is missing")
	}
	terminal := task.State == Merged || task.State == Cancelled
	if len(previous) > 1 {
		return fmt.Errorf("cannot reset amended task: expected one previous snapshot")
	}
	if len(previous) == 1 {
		old := previous[0]
		terminal = terminal || old.State == Merged || old.State == Cancelled
		if terminal && !TaskAmendmentReplacesArtifact(old, *task) {
			return fmt.Errorf("cannot reset amended task: terminal work requires a changed definition and newer artifact")
		}
	} else if terminal {
		return fmt.Errorf("cannot reset amended task: terminal work requires its previous snapshot")
	}
	if task.State != "" {
		if _, known := taskMoves[task.State]; !known {
			return fmt.Errorf("cannot reset amended task: unknown state %q", task.State)
		}
	}
	return setTaskState(task, Queued, nil, true)
}

func setTaskState(task *Task, next TaskState, previous *Task, amendmentReset bool) error {
	if task == nil {
		return fmt.Errorf("cannot change task state: task is missing")
	}
	if previous != nil && (previous.PlanTaskID != task.PlanTaskID || previous.State != next || task.State != Queued) {
		return fmt.Errorf("cannot restore amended task: state must match its previous snapshot")
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
		restore := previous != nil && previous.PlanTaskID == task.PlanTaskID && previous.State == next && task.State == Queued
		reset := amendmentReset && knownSource && next == Queued
		if !initial && !restore && !reset {
			return fmt.Errorf("task %s: %w; reload the task before retrying", task.PlanTaskID, err)
		}
	}
	task.State = next
	return nil
}

// TaskAmendmentReplacesArtifact binds a changed definition to the same task on
// a strictly newer artifact branch. Run persistence also checks the plan version.
func TaskAmendmentReplacesArtifact(old, next Task) bool {
	if old.PlanTaskID == "" || old.PlanTaskID != next.PlanTaskID || TaskDefinitionUnchanged(old, next) {
		return false
	}
	oldRun, oldRevision, oldOK := taskArtifact(old)
	nextRun, nextRevision, nextOK := taskArtifact(next)
	return oldOK && nextOK && oldRun == nextRun && nextRevision > oldRevision
}

func taskArtifact(task Task) (string, int64, bool) {
	parts := strings.Split(task.Branch, "/")
	if (len(parts) != 3 && len(parts) != 4) || parts[0] != statepath.RefName || parts[1] == "" || parts[len(parts)-1] != task.PlanTaskID {
		return "", 0, false
	}
	if len(parts) == 3 {
		return parts[1], 0, true
	}
	if !strings.HasPrefix(parts[2], "r") {
		return "", 0, false
	}
	revision, err := strconv.ParseInt(strings.TrimPrefix(parts[2], "r"), 10, 64)
	return parts[1], revision, err == nil && revision > 0 && parts[2] == fmt.Sprintf("r%d", revision)
}

// TaskDefinitionUnchanged compares the work requested by two task definitions.
func TaskDefinitionUnchanged(old, next Task) bool {
	if old.PlanTaskID != next.PlanTaskID || old.EffectiveType() != next.EffectiveType() || old.Instructions != next.Instructions || old.ExpectedOutput != next.ExpectedOutput ||
		!slices.Equal(old.Files, next.Files) || !slices.Equal(old.Criteria, next.Criteria) || !slices.Equal(old.DependsOn, next.DependsOn) || len(old.Checks) != len(next.Checks) {
		return false
	}
	for i := range old.Checks {
		if old.Checks[i].Command != next.Checks[i].Command || !slices.Equal(old.Checks[i].Criteria, next.Checks[i].Criteria) {
			return false
		}
	}
	return true
}
