package execution

import (
	"context"
	"fmt"
)

// RestoreResult reports a verified restore and how to undo it.
type RestoreResult struct {
	Record   CheckpointRecord
	Recovery CheckpointRecord
	Diff     SnapshotDiff
}

// PreviewRestore says what restoring a checkpoint would change in the project
// as it is now, without changing anything: "Added" files would be restored,
// "Removed" files deleted and "Changed" files overwritten.
func (e *Engine) PreviewRestore(ctx context.Context, checkpointID string) (SnapshotCheck, SnapshotDiff, error) {
	if e.checkpoints == nil {
		return SnapshotCheck{}, SnapshotDiff{}, fmt.Errorf("%w: checkpoint engine unavailable", ErrCheckpointFailed)
	}
	check, err := e.checkpoints.VerifySnapshot(checkpointID)
	if err != nil || check.State != SnapshotIntact {
		return check, SnapshotDiff{}, err
	}
	current, err := projectFiles(e.checkpoints.projectRoot, snapshotSkips)
	if err != nil {
		return check, SnapshotDiff{}, err
	}
	target, err := snapshotFiles(check.Record.WorktreePath)
	if err != nil {
		return check, SnapshotDiff{}, err
	}
	diff := diffFileMaps(current, target, 200)
	diff.From, diff.To = "current project", checkpointID
	return check, diff, nil
}

// snapshotSkips are the directories a snapshot never captures or restores.
var snapshotSkips = []string{".git", ".marshal"}

// RestoreVerified restores a checkpoint only when it is safe and bound to
// what the operator confirmed:
//   - no run of the project may be executing or hold active work, since any
//     writer could change files mid-restore;
//   - the confirmed digest must equal the snapshot's bound digest, and the
//     snapshot must still be intact;
//   - the current state is captured first as a recovery checkpoint;
//   - after restoring, every project file is re-read and must equal the
//     snapshot's files; otherwise the restore is reported as failed.
func (e *Engine) RestoreVerified(ctx context.Context, checkpointID, confirmedDigest string) (RestoreResult, error) {
	if e.checkpoints == nil {
		return RestoreResult{}, fmt.Errorf("%w: checkpoint engine unavailable", ErrCheckpointFailed)
	}
	e.operationMu.Lock()
	defer e.operationMu.Unlock()
	if n := len(e.activeRuns); n > 0 {
		return RestoreResult{}, fmt.Errorf("%w: %d run(s) are executing; pause or cancel them first", ErrCheckpointFailed, n)
	}
	runs, err := e.store.ListRuns(ctx)
	if err != nil {
		return RestoreResult{}, err
	}
	for _, run := range runs {
		if len(run.ActiveWorkers) > 0 || run.State == RunRunning || run.State == RunCancelling {
			return RestoreResult{}, fmt.Errorf("%w: run %s still has active work", ErrCheckpointFailed, run.RunID)
		}
		for _, task := range run.Tasks {
			switch task.State {
			case TaskAssigned, TaskRunning, TaskWaitingTool, TaskWaitingAgent:
				return RestoreResult{}, fmt.Errorf("%w: task %s of run %s is still %s", ErrCheckpointFailed, task.TaskID, run.RunID, task.State)
			}
		}
	}
	check, err := e.checkpoints.VerifySnapshot(checkpointID)
	if err != nil {
		return RestoreResult{}, err
	}
	if check.State != SnapshotIntact {
		return RestoreResult{}, fmt.Errorf("%w: checkpoint %s is %s and cannot be restored", ErrCheckpointFailed, checkpointID, check.State)
	}
	if confirmedDigest == "" || confirmedDigest != check.Record.SnapshotDigest {
		return RestoreResult{}, fmt.Errorf("%w: the confirmed digest does not match checkpoint %s", ErrCheckpointFailed, checkpointID)
	}
	recovery, err := e.checkpoints.CaptureCheckpoint(ctx, "operator", "pre-restore", "automatic recovery point before restoring "+checkpointID)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("%w: could not capture a recovery point, nothing was restored: %v", ErrCheckpointFailed, err)
	}
	diff, err := e.checkpoints.DiffSnapshots(recovery.CheckpointID, checkpointID, 200)
	if err != nil {
		return RestoreResult{}, err
	}
	restored, err := e.checkpoints.RestoreCheckpoint(ctx, checkpointID)
	if err != nil {
		return RestoreResult{Recovery: recovery}, err
	}
	want, err := snapshotFiles(restored.WorktreePath)
	if err != nil {
		return RestoreResult{Recovery: recovery}, err
	}
	got, err := projectFiles(e.checkpoints.projectRoot, snapshotSkips)
	if err != nil {
		return RestoreResult{Recovery: recovery}, err
	}
	if len(want) != len(got) {
		return RestoreResult{Recovery: recovery}, fmt.Errorf("%w: restored project has %d files, snapshot has %d; recover with %s", ErrCheckpointFailed, len(got), len(want), recovery.CheckpointID)
	}
	for path, sum := range want {
		if got[path] != sum {
			return RestoreResult{Recovery: recovery}, fmt.Errorf("%w: restored %s does not match the snapshot; recover with %s", ErrCheckpointFailed, path, recovery.CheckpointID)
		}
	}
	_, _ = e.journal.Append(JournalEvent{RunID: restored.RunID, Actor: "MARSHAL_ENGINE", EventType: "CHECKPOINT_RESTORED",
		Summary: fmt.Sprintf("restored %s; recovery point %s", checkpointID, recovery.CheckpointID)})
	return RestoreResult{Record: restored, Recovery: recovery, Diff: diff}, nil
}

// CaptureOperatorCheckpoint snapshots the project at the operator's request.
func (e *Engine) CaptureOperatorCheckpoint(ctx context.Context, reason string) (CheckpointRecord, error) {
	if e.checkpoints == nil {
		return CheckpointRecord{}, fmt.Errorf("%w: checkpoint engine unavailable", ErrCheckpointFailed)
	}
	return e.checkpoints.CaptureCheckpoint(ctx, "operator", "manual", reason)
}

// ListCheckpoints lists every valid snapshot of the project, newest first.
func (e *Engine) ListCheckpoints() ([]CheckpointRecord, error) {
	if e.checkpoints == nil {
		return nil, fmt.Errorf("%w: checkpoint engine unavailable", ErrCheckpointFailed)
	}
	return e.checkpoints.ListCheckpoints()
}
