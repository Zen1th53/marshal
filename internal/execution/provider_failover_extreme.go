//go:build !governed

package execution

import (
	"context"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/statepath"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/hostgit"
)

// RebindProviderFailover changes only the selected provider of an exact task.
// Its caller must verify the durable Marshal failover decision against the
// original approved plan. It never runs a worker or grants task/tool approval.
func (e *Engine) RebindProviderFailover(ctx context.Context, runID string, version int64, taskID, from, next, base, receipt string, authorize func(ExecutionRun, TaskExecution) error) (*ExecutionRun, error) {
	return e.rebindProviderFailover(ctx, runID, version, taskID, from, next, base, receipt, authorize, false)
}

// RebindApprovedProviderFailover derives its selector only from the current
// canonical plan's historical owner-reviewed producer snapshot. No model is
// accepted from the caller. It requires a fresh task approval afterward.
func (e *Engine) RebindApprovedProviderFailover(ctx context.Context, runID string, version int64, taskID, from, next, base, receipt string, authorize func(ExecutionRun, TaskExecution) error) (*ExecutionRun, error) {
	return e.rebindProviderFailover(ctx, runID, version, taskID, from, next, base, receipt, authorize, true)
}
func (e *Engine) rebindProviderFailover(ctx context.Context, runID string, version int64, taskID, from, next, base, receipt string, authorize func(ExecutionRun, TaskExecution) error, owned bool) (*ExecutionRun, error) {
	e.operationMu.Lock()
	defer e.operationMu.Unlock()
	if _, active := e.activeRuns[runID]; active {
		return nil, fmt.Errorf("%w: run is already executing", ErrInvalidStateTransition)
	}
	run, err := e.store.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	task, ok := run.Tasks[taskID]
	if !ok || version != run.Version || from == "" || next == "" || base == "" || receipt == "" || authorize == nil {
		return nil, fmt.Errorf("%w: incomplete or stale provider failover binding", ErrRunConflict)
	}
	if task.RunID != runID || (task.AssignedHarness != from && task.AssignedHarness != next) || (task.BaseCommit != "" && task.BaseCommit != base) {
		return nil, fmt.Errorf("%w: provider failover task binding differs", ErrRunInvalid)
	}
	if run.State != RunReady && run.State != RunPaused && run.State != RunFailed {
		return nil, fmt.Errorf("%w: provider failover run is %s", ErrInvalidStateTransition, run.State)
	}
	modelName := ""
	if !owned {
		var selected bool
		modelName, selected = verifiedFallbackModelFrom(ctx, next)
		if !selected {
			return nil, fmt.Errorf("%w: fallback requires the target provider's verified model", ErrRunConflict)
		}
	}
	if owned {
		if run.ParentRunID != "" || len(run.TaskLanes) != 0 || run.Mode == plan.ModeUltra {
			return nil, fmt.Errorf("%w: approved fallback requires an atomic parent and child binding", ErrRunBlocked)
		}
		modelName, err = e.approvedFallbackSelector(ctx, run, task, from, next)
		if err != nil {
			return nil, err
		}
	}
	if task.AssignedHarness == next && (task.State == TaskPending || task.State == TaskReady) {
		if task.AssignedModel != modelName {
			return nil, fmt.Errorf("%w: fallback replay model differs", ErrRunConflict)
		}
		history, historyErr := e.journal.GetEvents(runID)
		if historyErr != nil {
			return nil, historyErr
		}
		for _, entry := range history {
			if entry.TaskID == taskID && entry.EventType == "PROVIDER_FAILOVER_PREPARED" && entry.Summary == fmt.Sprintf("%s: %s -> %s", receipt, from, next) {
				if err := e.ValidateRunGoal(ctx, run); err != nil {
					return nil, err
				}
				if err := authorize(run, task); err != nil {
					return nil, err
				}
				return &run, nil
			}
		}
		return nil, fmt.Errorf("%w: provider failover receipt was not previously prepared", ErrRunConflict)
	}
	failed := task.State == TaskFailed
	if !failed && (task.Attempts != 0 || (task.State != TaskPending && task.State != TaskReady)) {
		return nil, fmt.Errorf("%w: task is not an unstarted or failed provider attempt", ErrInvalidStateTransition)
	}
	if failed && (run.Delivery != DeliveryPreserveBranch || task.WorktreePath == "" || task.BaseCommit != base) {
		return nil, fmt.Errorf("%w: failed provider attempt has no preserved binding", ErrRunInvalid)
	}
	for id, other := range run.Tasks {
		if id != taskID && (other.State == TaskFailed || other.State == TaskRunning || other.State == TaskWaitingTool || other.State == TaskNeedsApproval) {
			return nil, fmt.Errorf("%w: sibling task prevents provider failover", ErrRunBlocked)
		}
	}
	if err = e.ValidateRunGoal(ctx, run); err != nil {
		return nil, err
	}
	if _, err = e.GetHarness(next); err != nil {
		return nil, err
	}
	if err = authorize(run, task); err != nil {
		return nil, err
	}
	if _, err = e.journal.Append(JournalEvent{RunID: runID, TaskID: taskID, Actor: "MARSHAL_ENGINE", EventType: "PROVIDER_FAILOVER_PREPARED", Summary: fmt.Sprintf("%s: %s -> %s", receipt, from, next)}); err != nil {
		return nil, err
	}
	rollback := func() error { return nil }
	if failed {
		rollback, err = e.archiveLimitedProviderWorktree(ctx, run, task)
		if err != nil {
			return nil, err
		}
	}
	task.AssignedHarness = next
	task.AssignedAgent = ""
	task.AssignedModel = modelName
	if owned {
		task.ApprovalID = ""
		task.ApprovalRequired = true
	}
	task.NativeTurn = nil
	task.LeaseID = ""
	task.WorktreePath = ""
	task.ResultCommit = ""
	task.CompletedAt = nil
	task.StartedAt = nil
	task.RunRevision = 0
	task.CollectedEvidence = nil
	task.Alignment = nil
	task.LastFingerprint = ""
	task.LastFailureReason = ""
	if failed {
		task.State = TaskReady
		run.State = RunPaused
		run.CurrentPhase = PhasePaused
		run.EndedAt = nil
	}
	task.UpdatedAt = time.Now().UTC()
	run.Tasks[taskID] = task
	// Failure and budget history remain intact. A retry never hides consumption.
	if err = e.persistRun(ctx, &run); err != nil {
		return nil, errors.Join(err, rollback())
	}
	return &run, nil
}

// Keep the complete failed worktree, including dirty files, away from the clean
// canonical retry path. Restore both identities if the state CAS cannot commit.
func (e *Engine) archiveLimitedProviderWorktree(ctx context.Context, run ExecutionRun, task TaskExecution) (func() error, error) {
	expected := filepath.Join(e.cfg.ProjectRoot, statepath.DirName, "branches", taskWorktreeID(run, task))
	actual, err := filepath.Abs(task.WorktreePath)
	if err != nil {
		return nil, err
	}
	expected, err = filepath.Abs(expected)
	if err != nil {
		return nil, err
	}
	if actual != expected {
		return nil, fmt.Errorf("%w: failed worktree differs from canonical task", ErrIsolationCompromised)
	}
	archived := fmt.Sprintf("%s-limited-%d", actual, task.Attempts)
	branch := statepath.RefName + "/" + run.RunID + "/" + task.TaskID
	archiveBranch := fmt.Sprintf(statepath.RefName+"/%s/limited-%s-%d", run.RunID, task.TaskID, task.Attempts)
	git := func(dir string, args ...string) error {
		cmd, err := hostgit.Command(context.WithoutCancel(ctx), dir, args...)
		if err != nil {
			return err
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	if _, err = os.Stat(actual); os.IsNotExist(err) {
		state, inspectErr := e.branchManager().Inspect(ctx, archived)
		if inspectErr != nil || state.Branch != archiveBranch {
			return nil, fmt.Errorf("%w: failed provider archive is missing", ErrIsolationCompromised)
		}
		return func() error {
			if err := git(archived, "branch", "-m", branch); err != nil {
				return err
			}
			return git(e.cfg.ProjectRoot, "worktree", "move", archived, actual)
		}, nil
	}
	state, err := e.branchManager().Inspect(ctx, actual)
	if err != nil {
		return nil, err
	}
	if state.Branch != branch {
		return nil, fmt.Errorf("%w: failed provider worktree binding changed", ErrIsolationCompromised)
	}
	if err = git(actual, "merge-base", "--is-ancestor", task.BaseCommit, state.HEAD); err != nil {
		return nil, err
	}
	if err = git(e.cfg.ProjectRoot, "worktree", "move", actual, archived); err != nil {
		return nil, err
	}
	if err = git(archived, "branch", "-m", archiveBranch); err != nil {
		_ = git(e.cfg.ProjectRoot, "worktree", "move", archived, actual)
		return nil, err
	}
	return func() error {
		if err := git(archived, "branch", "-m", branch); err != nil {
			return err
		}
		return git(e.cfg.ProjectRoot, "worktree", "move", archived, actual)
	}, nil
}
