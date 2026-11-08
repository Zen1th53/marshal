//go:build !governed

package execution

import "context"

func (e *Engine) ValidateWorkerAttempt(ctx context.Context, run ExecutionRun, task TaskExecution) error {
	if run.ParentRunID != "" {
		parent, err := e.store.GetRun(ctx, run.ParentRunID)
		if err != nil {
			return err
		}
		original, ok := parent.Tasks[task.TaskID]
		if !ok || parent.State != RunRunning || parent.TaskLanes[task.TaskID] != run.RunID || run.ParentBinding != taskLaneBinding(parent, original, run.BaseCommit) {
			return ErrRunConflict
		}
	}
	return e.VerifyWorkerMutationLease(task)
}

// VerifyWorkerMutationLease reuses the original engine lease owner rules.
// Nonmutating tasks have no mutation lease; their supervised attempt still
// supplies the lifetime boundary checked by the host Runtime.
func (e *Engine) VerifyWorkerMutationLease(task TaskExecution) error {
	if !task.Mutates {
		return nil
	}
	owner := task.AssignedRole
	if owner == "" {
		owner = task.AssignedHarness
	}
	if owner == "" {
		owner = "worker-" + task.TaskID
	}
	return e.leases.VerifyOwner(task.TaskID, owner, task.LeaseID, nowUTC())
}
