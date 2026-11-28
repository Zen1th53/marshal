package store

import (
	"encoding/json"
	"fmt"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// Snapshot validation runs under the same transaction as revision checks.
// Initial snapshots retain persisted states, so importing saved runs never
// requires replaying historical work through the lifecycle.
func checkTaskSnapshotWrite[T any](previous []byte, value T) error {
	switch next := any(value).(type) {
	case marshal.Task:
		var old marshal.Task
		if err := json.Unmarshal(previous, &old); err != nil {
			return err
		}
		return marshal.SetTaskState(&old, next.State)
	case marshal.Run:
		var old marshal.Run
		if err := json.Unmarshal(previous, &old); err != nil {
			return err
		}
		byID := map[string]marshal.Task{}
		for _, task := range old.Tasks {
			byID[task.PlanTaskID] = task
		}
		for _, task := range next.Tasks {
			prior, exists := byID[task.PlanTaskID]
			if !exists {
				continue
			}
			// Only a newer plan replacing this task's definition and artifact may
			// reopen terminal work. A run-wide revision alone is insufficient.
			replacement := next.PlanVersion > old.PlanVersion && task.State == marshal.Queued && marshal.TaskAmendmentReplacesArtifact(prior, task)
			if (prior.State == marshal.Merged || prior.State == marshal.Cancelled) && task.State != prior.State && !replacement {
				return fmt.Errorf("cannot save task %s: terminal work is final", task.PlanTaskID)
			}
			if next.PlanVersion > old.PlanVersion && task.State == marshal.Queued {
				reset := task
				if err := marshal.ResetTaskForAmendment(&reset, prior); err != nil {
					return err
				}
			} else if err := marshal.SetTaskState(&prior, task.State); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("cannot save task state: unsupported snapshot type %T", value)
	}
	return nil
}
