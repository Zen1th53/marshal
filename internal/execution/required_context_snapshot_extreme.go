//go:build !governed

package execution

import (
	"context"
	"os"
	"path/filepath"
	"reflect"

	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/statepath"
)

// ValidateRequiredContextFenceTask validates DATA from current detached
// snapshots supplied by WithRequiredContextFence. It conveys no authority;
// caller-created maps cannot produce an invocation grant. The trusted caller
// must compare its exact prepared binding and current authorization generation
// and perform durable context admission before creating a one-shot token.
//
// This helper never reenters engine/store admission or lane locks. Goal/plan
// readers and the separate lease/worktree managers are read before a caller's
// SQLite admission transaction; callbacks must not begin a transaction first.
func (e *Engine) ValidateRequiredContextFenceTask(ctx context.Context, snapshots map[string]ExecutionRun, runID, taskID string) (ExecutionRun, TaskExecution, error) {
	var zeroRun ExecutionRun
	var zeroTask TaskExecution
	if e == nil || e.goalReader == nil || e.planReader == nil || runID == "" || taskID == "" || len(snapshots) == 0 || len(snapshots) > 256 {
		return zeroRun, zeroTask, ErrRunBlocked
	}
	if err := ctx.Err(); err != nil {
		return zeroRun, zeroTask, err
	}
	run, exists := snapshots[runID]
	if !exists || run.RunID != runID || run.Version < 1 || run.ProjectID == "" || run.SessionID == "" || run.GoalID == "" || run.GoalRevision < 1 || run.PlanID == "" || run.PlanVersion < 1 || !requiredSnapshotActiveRun(run.State) {
		return zeroRun, zeroTask, ErrRunConflict
	}
	root, err := filepath.EvalSymlinks(e.cfg.ProjectRoot)
	if err != nil || root != run.Provenance.RepoRoot {
		return zeroRun, zeroTask, ErrIsolationCompromised
	}
	task, exists := run.Tasks[taskID]
	if !exists || task.TaskID != taskID || task.RunID != runID || task.CanonicalTaskID == "" || task.RunRevision < 1 || task.RunRevision > run.Version || task.Attempts < 1 || task.AssignedHarness == "" || task.AssignedModel == "" || task.WorktreePath == "" || task.ResultCommit != "" {
		return zeroRun, zeroTask, ErrRunConflict
	}
	if task.State != TaskRunning && task.State != TaskWaitingTool && task.State != TaskNeedsApproval && task.State != TaskReady {
		return zeroRun, zeroTask, ErrRunConflict
	}
	if task.NativeTurn != nil {
		native := task.NativeTurn
		expected := ""
		switch task.AssignedHarness {
		case "codex":
			expected = "codex-app-server"
		case "claude":
			expected = "claude-stream"
		}
		if expected == "" || native.Provider != expected || native.ThreadID == "" || native.TurnID == "" || native.Worktree != task.WorktreePath || native.WorktreeDigest == "" || native.ConstraintDigest == "" || native.RunRevision != task.RunRevision || native.State != "RUNNING" && native.State != "APPROVAL_REQUIRED" {
			return zeroRun, zeroTask, ErrRunConflict
		}
	}
	if task.State == TaskReady {
		// READY ordinarily authorizes nothing. Only a retained native turn whose
		// exact approval was already consumed may be inspected for native resume.
		if run.State != RunRunning || task.NativeTurn == nil || task.NativeTurn.State != "APPROVAL_REQUIRED" || task.ApprovalID == "" || e.approvals == nil {
			return zeroRun, zeroTask, ErrRunConflict
		}
		approval, err := e.approvals.GetApproval(task.ApprovalID)
		if err != nil || approval.Status != ApprovalConsumed || !IsNativeProviderApproval(approval.OperationType) || approval.RunID != runID || approval.TaskID != taskID || approval.PlanID != run.PlanID || approval.PlanVersion != run.PlanVersion {
			return zeroRun, zeroTask, ErrRunConflict
		}
		if task.NativeTurn.Provider == "codex-app-server" && approval.OperationType != "CODEX_APP_SERVER_NATIVE" || task.NativeTurn.Provider == "claude-stream" && approval.OperationType != "CLAUDE_STREAM_NATIVE" {
			return zeroRun, zeroTask, ErrRunConflict
		}
	}
	if run.ParentRunID != "" {
		parent, ok := snapshots[run.ParentRunID]
		original, exists := parent.Tasks[taskID]
		if !ok || !exists || parent.RunID != run.ParentRunID || parent.Version < 1 || parent.ParentRunID != "" || !requiredSnapshotActiveRun(parent.State) || run.LaneTaskID != taskID || run.SelectedTask != taskID || parent.TaskLanes[taskID] != runID || len(run.TaskLanes) != 0 || original.RunID != parent.RunID || original.TaskID != taskID || run.ParentBinding != taskLaneBinding(parent, original, run.BaseCommit) {
			return zeroRun, zeroTask, ErrRunConflict
		}
		if run.ProjectID != parent.ProjectID || run.SessionID != parent.SessionID || run.PlanID != parent.PlanID || run.PlanVersion != parent.PlanVersion || run.GoalID != parent.GoalID || run.GoalRevision != parent.GoalRevision || parent.Provenance.RepoRoot != root || run.Mode != plan.ModeUltra || parent.Mode != plan.ModeUltra || run.Delivery != DeliveryPreserveBranch || parent.Delivery != DeliveryPreserveBranch || run.BaseCommit == "" || task.BaseCommit != run.BaseCommit || !requiredSnapshotSameTaskDefinition(original, task) {
			return zeroRun, zeroTask, ErrRunConflict
		}
	} else if len(run.TaskLanes) != 0 || run.LaneTaskID != "" || run.ParentBinding != "" {
		return zeroRun, zeroTask, ErrRunConflict
	}
	if err = e.requiredSnapshotWorkspace(root, run, task); err != nil {
		return zeroRun, zeroTask, err
	}
	// ValidateRunGoal only invokes the canonical goal reader. Unlike
	// ValidateWorkerAttempt/ValidateTaskLaneBinding it never reads a run store.
	if err = e.ValidateRunGoal(ctx, run); err != nil {
		return zeroRun, zeroTask, err
	}
	p, err := e.planReader.GetActivePlan(ctx, run.ProjectID)
	if err != nil || p.ID != run.PlanID || p.Version != run.PlanVersion || p.ProjectID != run.ProjectID || p.State != plan.StateApproved || p.Goal.GoalID != run.GoalID || p.Goal.Revision != run.GoalRevision {
		return zeroRun, zeroTask, ErrRunConflict
	}
	approvedTask := false
	for _, planned := range p.Tasks {
		if planned.ID == taskID {
			approvedTask = planned.Title == task.Description && planned.Mutating == task.Mutates && reflect.DeepEqual(planned.Paths, task.TargetFiles)
			break
		}
	}
	if !approvedTask {
		return zeroRun, zeroTask, ErrRunConflict
	}
	if task.Mutates && e.leases == nil {
		return zeroRun, zeroTask, ErrLeaseExpired
	}
	if err = e.VerifyWorkerMutationLease(task); err != nil {
		return zeroRun, zeroTask, err
	}
	if err = ctx.Err(); err != nil {
		return zeroRun, zeroTask, err
	}
	return run, task, nil
}

func requiredSnapshotActiveRun(state RunState) bool {
	return state == RunRunning || state == RunNeedsApproval
}
func requiredSnapshotSameTaskDefinition(a, b TaskExecution) bool {
	// Run/attempt/lease/result state differs between the immutable parent
	// definition and its executing child. Approved semantic fields must agree.
	// A provider can discover an approval while executing the child, but a child
	// cannot lower an approval requirement inherited from its parent.
	return a.TaskID == b.TaskID && a.CanonicalTaskID == b.CanonicalTaskID && a.Description == b.Description && a.AssignedRole == b.AssignedRole && a.AssignedAgent == b.AssignedAgent && a.AssignedHarness == b.AssignedHarness && a.AssignedModel == b.AssignedModel && a.Mutates == b.Mutates && (!a.ApprovalRequired || b.ApprovalRequired) && reflect.DeepEqual(a.TargetFiles, b.TargetFiles) && reflect.DeepEqual(a.Dependencies, b.Dependencies) && reflect.DeepEqual(a.RequiredEvidence, b.RequiredEvidence) && reflect.DeepEqual(a.OutputArtifacts, b.OutputArtifacts) && reflect.DeepEqual(a.FallbackHarnesses, b.FallbackHarnesses) && reflect.DeepEqual(a.CheckpointsBefore, b.CheckpointsBefore)
}
func (e *Engine) requiredSnapshotWorkspace(root string, run ExecutionRun, task TaskExecution) error {
	if !filepath.IsAbs(task.WorktreePath) || filepath.Clean(task.WorktreePath) != task.WorktreePath {
		return ErrIsolationCompromised
	}
	resolved, err := filepath.EvalSymlinks(task.WorktreePath)
	if err != nil || resolved != task.WorktreePath {
		return ErrIsolationCompromised
	}
	info, err := os.Lstat(task.WorktreePath)
	if err != nil || !info.IsDir() {
		return ErrIsolationCompromised
	}
	if run.Delivery == DeliveryPreserveBranch {
		if task.WorktreePath != filepath.Join(root, statepath.DirName, "branches", taskWorktreeID(run, task)) || task.BaseCommit == "" {
			return ErrIsolationCompromised
		}
		return nil
	}
	if e.worktrees == nil {
		return ErrIsolationCompromised
	}
	wm := e.worktrees
	wm.mu.Lock()
	defer wm.mu.Unlock()
	owned := wm.owned[task.WorktreePath]
	if owned == nil || wm.projectRoot != root || wm.workspaces[run.RunID+"\x00"+task.TaskID] != task.WorktreePath || !os.SameFile(owned, info) {
		return ErrIsolationCompromised
	}
	return nil
}
