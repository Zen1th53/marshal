//go:build !governed

package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/Zen1th53/marshal/internal/hostgit"
	"github.com/Zen1th53/marshal/internal/plan"
)

// Detached snapshot validation is DATA ONLY. Only WithRequiredContextFence
// proves these snapshots belong to the currently owned Engine/store instance.
// No model, network, run-store reentry or SQLite transaction occurs here.
func (e *Engine) ValidateRequiredContextFenceCompletion(ctx context.Context, snapshots map[string]ExecutionRun, rootRunID string) (ExecutionRun, map[string]TaskExecution, error) {
	zero := ExecutionRun{}
	if err := ctx.Err(); err != nil {
		return zero, nil, err
	}
	if e == nil || e.goalReader == nil || e.planReader == nil || rootRunID == "" || len(snapshots) == 0 || len(snapshots) > 256 {
		return zero, nil, ErrRunBlocked
	}
	root, ok := snapshots[rootRunID]
	if !ok || root.RunID != rootRunID || root.Version < 1 || root.ParentRunID != "" || root.LaneTaskID != "" || root.ParentBinding != "" || root.State != RunDonePendingVerification || root.CurrentPhase != PhaseCompletedPending || root.ProjectID == "" || root.SessionID == "" || root.GoalID == "" || root.GoalRevision < 1 || root.PlanID == "" || root.PlanVersion < 1 || len(root.Tasks) == 0 {
		return zero, nil, fmt.Errorf("%w: required completion root identity/state", ErrRunConflict)
	}
	projectRoot, err := filepath.EvalSymlinks(e.cfg.ProjectRoot)
	if err != nil || projectRoot != root.Provenance.RepoRoot {
		return zero, nil, ErrIsolationCompromised
	}
	if err = e.ValidateRunGoal(ctx, root); err != nil {
		return zero, nil, err
	}
	p, err := e.planReader.GetActivePlan(ctx, root.ProjectID)
	if err != nil || p.ID != root.PlanID || p.Version != root.PlanVersion || p.ProjectID != root.ProjectID || p.State != plan.StateApproved || p.Goal.GoalID != root.GoalID || p.Goal.Revision != root.GoalRevision || len(p.Tasks) != len(root.Tasks) || !requiredCompletionObject(root.BaseCommit) {
		return zero, nil, fmt.Errorf("%w: required completion canonical Plan", ErrRunConflict)
	}
	expectedSnapshots := 1
	if len(root.TaskLanes) > 0 {
		if root.Mode != plan.ModeUltra || root.Delivery != DeliveryPreserveBranch || len(root.TaskLanes) != len(root.Tasks) || len(root.TaskLaneStates) != len(root.Tasks) {
			return zero, nil, ErrRunConflict
		}
		expectedSnapshots += len(root.TaskLanes)
	}
	if len(snapshots) != expectedSnapshots {
		return zero, nil, ErrRunConflict
	}
	completed := make(map[string]TaskExecution, len(p.Tasks))
	seenLanes := map[string]bool{}
	for _, planned := range p.Tasks {
		original, exists := root.Tasks[planned.ID]
		if !exists || original.TaskID != planned.ID || original.RunID != root.RunID || original.CanonicalTaskID != requiredCompletionCanonicalTaskID(p.ID, p.Version, planned.ID) || original.Description != planned.Title || original.Mutates != planned.Mutating || !reflect.DeepEqual(original.TargetFiles, planned.Paths) || !reflect.DeepEqual(original.Dependencies, planned.DependsOn) {
			return zero, nil, fmt.Errorf("%w: required completion approved task", ErrRunConflict)
		}
		owner, task := root, original
		if len(root.TaskLanes) > 0 {
			laneID := root.TaskLanes[planned.ID]
			lane, found := snapshots[laneID]
			if !found || laneID == root.RunID || seenLanes[laneID] || lane.RunID != laneID || lane.ParentRunID != root.RunID || lane.Version < 1 || lane.LaneTaskID != planned.ID || lane.SelectedTask != "" || len(lane.TaskLanes) != 0 || lane.State != RunDonePendingVerification || lane.CurrentPhase != PhaseCompletedPending || lane.ProjectID != root.ProjectID || lane.SessionID != root.SessionID || lane.GoalID != root.GoalID || lane.GoalRevision != root.GoalRevision || lane.PlanID != root.PlanID || lane.PlanVersion != root.PlanVersion || lane.Mode != root.Mode || lane.Delivery != root.Delivery || lane.Provenance.RepoRoot != projectRoot || lane.BaseCommit == "" || lane.ParentBinding != taskLaneBinding(root, original, lane.BaseCommit) || root.TaskLaneStates[planned.ID] != TaskCompletedPendingVerify {
				return zero, nil, fmt.Errorf("%w: required completion child identity/state", ErrRunConflict)
			}
			seenLanes[laneID] = true
			var taskExists bool
			task, taskExists = lane.Tasks[planned.ID]
			if !taskExists || !requiredSnapshotSameTaskDefinition(original, task) {
				return zero, nil, fmt.Errorf("%w: required completion child definition", ErrRunConflict)
			}
			owner = lane
		}
		if task.RunID != owner.RunID || task.TaskID != planned.ID || task.State != TaskCompletedPendingVerify || task.RunRevision < 1 || task.RunRevision > owner.Version || task.Attempts < 1 || task.AssignedHarness == "" || task.AssignedModel == "" || task.CompletedAt == nil || task.StartedAt == nil || task.CompletedAt.Before(*task.StartedAt) || task.CompletedAt.After(nowUTC()) || task.LastFailureReason != "" {
			return zero, nil, fmt.Errorf("%w: required completion finished task metadata", ErrRunConflict)
		}
		if task.NativeTurn != nil && task.NativeTurn.State != "COMPLETED" && task.NativeTurn.State != "DONE" {
			return zero, nil, ErrRunConflict
		}
		if owner.Delivery != DeliveryPreserveBranch {
			// The conventional engine deletes its WM identity during successful
			// cleanup. Without a trusted cleanup witness, a historical path is
			// UNKNOWN; path prefixes alone do not prove runtime ownership.
			return zero, nil, ErrIsolationCompromised
		}
		if !requiredCompletionObject(task.BaseCommit) || owner.ParentRunID != "" && task.BaseCommit != owner.BaseCommit || !requiredCompletionObject(task.ResultCommit) || task.Mutates && task.ResultCommit == task.BaseCommit {
			return zero, nil, fmt.Errorf("%w: required completion branch result/base", ErrRunConflict)
		}
		if err = e.requiredSnapshotWorkspace(projectRoot, owner, task); err != nil {
			return zero, nil, err
		}
		if err = requiredCompletionRetainedWorkspace(ctx, projectRoot, task); err != nil {
			return zero, nil, err
		}
		if err = e.requiredCompletionReleasedLease(owner, task); err != nil {
			return zero, nil, err
		}
		completed[task.TaskID] = task
	}
	// The initial parent delivery base remains canonical for a sequential
	// preserve-branch run. Later selected tasks can start from a real merged
	// dependency result, so their immutable per-task base need not equal it.
	// Every task and dependency below came from the current approved Plan and
	// the owned snapshots above; no caller-supplied dependency map is accepted.
	for _, planned := range p.Tasks {
		task := completed[planned.ID]
		if err = requiredCompletionAncestry(ctx, projectRoot, root.BaseCommit, task.BaseCommit); err != nil {
			return zero, nil, err
		}
		if err = requiredCompletionAncestry(ctx, projectRoot, task.BaseCommit, task.ResultCommit); err != nil {
			return zero, nil, err
		}
		for _, dependencyID := range planned.DependsOn {
			dependency, exists := completed[dependencyID]
			if !exists || dependencyID == planned.ID {
				return zero, nil, ErrRunConflict
			}
			if err = requiredCompletionAncestry(ctx, projectRoot, dependency.ResultCommit, task.BaseCommit); err != nil {
				return zero, nil, err
			}
		}
	}
	if err = ctx.Err(); err != nil {
		return zero, nil, err
	}
	return root, completed, nil
}

func (e *Engine) requiredCompletionReleasedLease(run ExecutionRun, task TaskExecution) error {
	if !task.Mutates {
		return nil
	}
	if e.leases == nil || task.LeaseID == "" {
		return ErrLeaseExpired
	}
	owner := task.AssignedRole
	if owner == "" {
		owner = task.AssignedHarness
	}
	if owner == "" {
		owner = "worker-" + task.TaskID
	}
	e.leases.mu.RLock()
	defer e.leases.mu.RUnlock()
	lease := e.leases.leases[task.LeaseID]
	if lease == nil && task.CompletedAt != nil && !e.startedAt.IsZero() && task.CompletedAt.Before(e.startedAt) {
		// Leases are process-local. A task that finished before this runtime
		// process started held its lease in an earlier process, which ended:
		// no worker of this process can hold it. Its workspace, result and
		// ancestry are still checked above and below.
		return nil
	}
	if lease == nil || lease.LeaseID != task.LeaseID || lease.RunID != run.RunID || lease.TaskID != task.TaskID || lease.AgentID != owner || lease.Role != owner || lease.Status != LeaseReleased || !reflect.DeepEqual(lease.ScopedResources, task.TargetFiles) {
		return ErrLeaseExpired
	}
	return nil
}

func requiredCompletionRetainedWorkspace(ctx context.Context, projectRoot string, task TaskExecution) error {
	git := func(root string, args ...string) (string, error) {
		cmd, err := hostgit.Command(ctx, root, args...)
		if err != nil {
			return "", err
		}
		raw, err := cmd.Output()
		if err != nil {
			return "", err
		}
		if len(raw) > 8192 {
			return "", ErrIsolationCompromised
		}
		return strings.TrimSpace(string(raw)), nil
	}
	head, err := git(task.WorktreePath, "rev-parse", "HEAD")
	if err != nil || head != task.ResultCommit {
		return ErrIsolationCompromised
	}
	status, err := git(task.WorktreePath, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil || status != "" {
		return ErrIsolationCompromised
	}
	common, err := git(task.WorktreePath, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return ErrIsolationCompromised
	}
	rootCommon, err := git(projectRoot, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil || common != rootCommon {
		return ErrIsolationCompromised
	}
	info, err := os.Lstat(task.WorktreePath)
	if err != nil || !info.IsDir() {
		return ErrIsolationCompromised
	}
	return nil
}

func requiredCompletionCanonicalTaskID(planID string, version int64, taskID string) string {
	sum := sha256.Sum256([]byte(planID + "\x00" + fmt.Sprint(version) + "\x00" + taskID))
	return "TASK-P05-" + hex.EncodeToString(sum[:12])
}
func requiredCompletionObject(value string) bool {
	if (len(value) != 40 && len(value) != 64) || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// Only full object IDs from owned snapshots enter these fixed local queries.
// Validate commit object type as well: a reachable tree/blob is not a source
// baseline. No shell, network, Engine/store reentry, or writer transaction.
func requiredCompletionAncestry(ctx context.Context, repository, before, after string) error {
	if !requiredCompletionObject(before) || !requiredCompletionObject(after) {
		return ErrRunConflict
	}
	for _, id := range []string{before, after} {
		cmd, err := hostgit.Command(ctx, repository, "cat-file", "-t", id)
		if err != nil {
			return err
		}
		kind, err := cmd.Output()
		if err != nil || string(kind) != "commit\n" {
			return fmt.Errorf("%w: required completion commit identity", ErrRunConflict)
		}
	}
	cmd, err := hostgit.Command(ctx, repository, "merge-base", "--is-ancestor", before, after)
	if err != nil {
		return err
	}
	if err = cmd.Run(); err != nil {
		return fmt.Errorf("%w: required completion source ancestry", ErrRunConflict)
	}
	return nil
}
