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
			// A new artifact after a plan amendment explicitly resets changed work.
			if next.PlanVersion > old.PlanVersion && task.State == marshal.Queued {
				if err := marshal.ResetTaskForAmendment(&prior); err != nil {
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
