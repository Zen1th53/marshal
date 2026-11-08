//go:build !governed

package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/plan"
)

func taskWorktreeID(run ExecutionRun, task TaskExecution) string {
	if run.ParentRunID == "" {
		return branchTaskID(task.TaskID)
	}
	return branchTaskID(run.RunID)
}

// Parent binding excludes mutable execution state and child pointers. Every
// lane retains the exact approved task definition, goal, plan, project and root.
func taskLaneBinding(run ExecutionRun, task TaskExecution, base string) string {
	data, _ := json.Marshal(struct {
		Root, Parent, Goal, Plan, Base string
		GoalRevision, PlanVersion      int64
		Task                           TaskExecution
	}{run.Provenance.RepoRoot, run.RunID, run.GoalID, run.PlanID, base, run.GoalRevision, run.PlanVersion, task})
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func laneScopesOverlap(a, b []string) bool {
	for _, group := range [][]string{a, b} {
		if len(group) == 0 {
			return true
		}
		for _, path := range group {
			if path == "" || path == "." || filepath.IsAbs(path) || !filepath.IsLocal(path) || strings.ContainsAny(path, "*?[") {
				return true
			}
		}
	}
	return ResourcesOverlap(a, b)
}

// OpenTaskLane reserves one durable execution domain from the original parent.
// It never approves a task or starts a provider. Successful dependencies are
// copied only from canonical completed sibling lanes, retaining their evidence.
func (e *Engine) OpenTaskLane(ctx context.Context, parentID, taskID, base string) (*ExecutionRun, error) {
	return e.OpenTaskLaneRework(ctx, parentID, taskID, base, 0)
}

// taskLaneID is the deterministic lane identity of one task attempt. Rework 0
// keeps the original identity; each rework of a returned task gets its own.
func taskLaneID(parentID, taskID string, rework int) string {
	key := parentID + "\x00" + taskID
	if rework > 0 {
		key += fmt.Sprintf("\x00rework-%d", rework)
	}
	h := sha256.Sum256([]byte(key))
	return "lane-" + hex.EncodeToString(h[:16])
}

// TaskLaneID reports the lane identity of a task attempt under a parent run.
func TaskLaneID(parentID, taskID string, rework int) string {
	return taskLaneID(parentID, taskID, rework)
}

// OpenTaskLaneRework reserves the execution domain for one attempt of a task.
// Rework n > 0 is a returned task's next attempt inside the same approved
// parent: it opens a fresh lane from the same original task binding and base,
// only directly after attempt n-1, and only once that lane no longer runs.
// The previous lane and its evidence are retained; the parent points at the
// newest attempt, so a stale lane cannot reserve calls or transport again.
func (e *Engine) OpenTaskLaneRework(ctx context.Context, parentID, taskID, base string, rework int) (*ExecutionRun, error) {
	if rework < 0 || rework > maxTaskLaneReworks {
		return nil, fmt.Errorf("%w: task lane rework out of range", ErrRunInvalid)
	}
	e.operationMu.Lock()
	defer e.operationMu.Unlock()
	e.laneCommitMu.Lock()
	defer e.laneCommitMu.Unlock()
	parent, err := e.store.GetRun(ctx, parentID)
	if err != nil {
		return nil, err
	}
	// A rework may follow the last lane's completion: the parent summary is then
	// done pending verification and reopens for the returned task's attempt.
	reopen := rework > 0 && parent.State == RunDonePendingVerification
	if parent.ParentRunID != "" || parent.Mode != plan.ModeUltra || parent.Delivery != DeliveryPreserveBranch || base == "" || (parent.State.IsTerminal() && !reopen) || parent.State == RunPaused {
		return nil, fmt.Errorf("%w: invalid task lane parent", ErrRunInvalid)
	}
	canonicalRoot, rootErr := filepath.EvalSymlinks(e.cfg.ProjectRoot)
	boundRoot, boundErr := filepath.EvalSymlinks(parent.Provenance.RepoRoot)
	if rootErr != nil || boundErr != nil || canonicalRoot != boundRoot {
		return nil, ErrIsolationCompromised
	}
	if err = e.ValidateRunGoal(ctx, parent); err != nil {
		return nil, err
	}
	task, ok := parent.Tasks[taskID]
	if !ok || task.RunID != parentID || task.Attempts != 0 || task.State != TaskPending || (task.BaseCommit != "" && task.BaseCommit != base) {
		return nil, fmt.Errorf("%w: original task binding changed", ErrRunInvalid)
	}
	if _, active := e.activeRuns[parentID]; active {
		return nil, fmt.Errorf("%w: whole parent is already executing", ErrInvalidStateTransition)
	}
	binding := taskLaneBinding(parent, task, base)
	id := taskLaneID(parentID, taskID, rework)
	var orphan *ExecutionRun
	if existing, readErr := e.store.GetRun(ctx, id); readErr == nil {
		if existing.ParentRunID != parentID || existing.ParentBinding != binding || existing.LaneTaskID != taskID || existing.BaseCommit != base {
			return nil, fmt.Errorf("%w: orphan lane identity differs", ErrRunConflict)
		}
		if _, live := e.activeRuns[id]; !live && (existing.State == RunRunning || existing.State == RunCancelling) {
			return nil, fmt.Errorf("%w: orphan task lane requires explicit reconciliation", ErrRunBlocked)
		}
		orphan = &existing
	} else if !errors.Is(readErr, ErrRunNotFound) {
		return nil, readErr
	}
	if current := parent.TaskLanes[taskID]; current != "" {
		lane, err := e.store.GetRun(ctx, current)
		if err != nil {
			return nil, err
		}
		// A provider switch supersedes the previous attempt's binding; that
		// attempt may precede the switched one but is never resumed.
		switched := current != id && slices.Contains(parent.TaskProviderSwitches[taskID].PriorBindings, lane.ParentBinding)
		if lane.ParentRunID != parentID || lane.ParentBinding != binding && !switched || lane.LaneTaskID != taskID || lane.BaseCommit != base {
			return nil, fmt.Errorf("%w: durable task lane binding differs", ErrRunConflict)
		}
		_, active := e.activeRuns[current]
		if current == id {
			if !active && (lane.State == RunRunning || lane.State == RunCancelling) {
				return nil, fmt.Errorf("%w: orphan task lane requires explicit reconciliation", ErrRunBlocked)
			}
			return &lane, nil
		}
		// The current lane must be an earlier attempt of this task. Attempt
		// numbers between it and this one may be missing: a dispatch refused
		// before its lane opened leaves no lane, so nothing ran under them.
		if !earlierTaskLaneAttempt(parentID, taskID, current, rework) {
			return nil, fmt.Errorf("%w: task lane rework is out of order", ErrRunConflict)
		}
		if !active && unstartedTaskLane(lane, taskID) {
			// A lane opened for an attempt that never started (its dispatch
			// was refused before any worker ran) is superseded, not resumed.
			if err := e.supersedeUnstartedTaskLane(ctx, lane, taskID); err != nil {
				return nil, err
			}
		} else if !active && orphanTaskLane(lane, taskID) {
			// A lane still recorded as running that no executor of this
			// runtime owns was interrupted by a restart. It is paused with its
			// worktree and evidence retained, and the next attempt opens.
			if err := e.pauseOrphanTaskLane(ctx, lane, taskID); err != nil {
				return nil, err
			}
		} else if active || (lane.State != RunDonePendingVerification && lane.State != RunFailed && lane.State != RunPaused) {
			return nil, fmt.Errorf("%w: previous task lane attempt is %s", ErrRunBlocked, lane.State)
		}
	} else if rework > 0 {
		return nil, fmt.Errorf("%w: task lane rework has no previous attempt", ErrRunConflict)
	}
	runs, err := e.store.ListRuns(ctx)
	if err != nil {
		return nil, err
	}
	active := 0
	for _, run := range runs {
		if run.RunID == parentID || run.RunID == id || run.State.IsTerminal() || run.State == RunDonePendingVerification {
			continue
		}
		if run.ParentRunID == "" {
			// Lane parents are summaries; their children below own admission.
			// A conventional active run still owns its unresolved task scopes.
			if len(run.TaskLanes) > 0 || (run.State != RunRunning && run.State != RunNeedsApproval && run.State != RunCancelling) {
				continue
			}
			for _, other := range run.Tasks {
				if !other.State.IsTerminal() && other.State != TaskCompletedPendingVerify && laneScopesOverlap(task.TargetFiles, other.TargetFiles) {
					return nil, fmt.Errorf("%w: active conventional run scope conflicts", ErrLeaseConflict)
				}
			}
			active++
			continue
		}
		other := run.Tasks[run.LaneTaskID]
		if other.State == TaskCompletedPendingVerify || other.State.IsTerminal() {
			continue
		}
		active++
		if laneScopesOverlap(task.TargetFiles, other.TargetFiles) {
			return nil, fmt.Errorf("%w: task lane scope conflicts with %s", ErrLeaseConflict, other.TaskID)
		}
	}
	if active >= e.cfg.MaxWorkers {
		return nil, fmt.Errorf("%w: task lane capacity exhausted", ErrRunBlocked)
	}
	// Completed dependency values are real snapshots of their owning runs.
	deps := make(map[string]TaskExecution)
	var collect func(string) error
	collect = func(id string) error {
		if _, ok := deps[id]; ok {
			return nil
		}
		dep, err := e.store.GetRun(ctx, parent.TaskLanes[id])
		if err != nil {
			return fmt.Errorf("%w: dependency %s has no durable lane", ErrRunBlocked, id)
		}
		value, ok := dep.Tasks[id]
		original, exists := parent.Tasks[id]
		if !ok || !exists || dep.ParentRunID != parentID || dep.ParentBinding != taskLaneBinding(parent, original, dep.BaseCommit) || value.State != TaskCompletedPendingVerify || value.ResultCommit == "" {
			return fmt.Errorf("%w: dependency %s is not a checked completed lane", ErrRunBlocked, id)
		}
		deps[id] = value
		for _, nested := range value.Dependencies {
			if err := collect(nested); err != nil {
				return err
			}
		}
		return nil
	}
	for _, dep := range task.Dependencies {
		if err := collect(dep); err != nil {
			return nil, err
		}
	}
	lane, err := cloneExecutionRun(parent)
	if err != nil {
		return nil, err
	}
	lane.RunID = id
	lane.ParentRunID = parentID
	lane.ParentBinding = binding
	lane.TaskLanes = nil
	lane.TaskLaneStates = nil
	lane.ParallelCallsReserved = 0
	lane.ParallelRetriesReserved = 0
	lane.SelectedTask = taskID
	lane.LaneTaskID = taskID
	lane.BaseCommit = base
	lane.Version = 1
	lane.State = RunReady
	lane.CurrentPhase = PhaseInit
	lane.BudgetConsumed = BudgetUsage{}
	lane.Evidence = nil
	lane.EvidenceRecords = nil
	lane.ClaimRecords = nil
	lane.Failures = nil
	lane.Checkpoints = nil
	lane.ProviderCalls = nil
	task.RunID = id
	task.BaseCommit = base
	deps[taskID] = task
	lane.Tasks = deps
	// A interrupted parent-pointer write is repaired only by exact deterministic
	// identity, never by inferring authority from an arbitrary file or lane label.
	if orphan != nil {
		lane = *orphan
	} else if err = e.store.CreateRun(ctx, lane); err != nil {
		return nil, err
	}
	if parent.TaskLanes == nil {
		parent.TaskLanes = make(map[string]string)
	}
	parent.TaskLanes[taskID] = id
	parent.State = RunRunning
	parent.CurrentPhase = PhaseExecuting
	parent.EndedAt = nil
	if err = e.persistLaneSnapshotLocked(ctx, &parent); err != nil {
		return nil, err
	}
	return &lane, nil
}

// Reserve a model call on the durable parent before invoking the provider. An
// abrupt interruption retains the conservative reservation instead of hiding
// spent calls. Sibling lanes cannot each receive the entire remaining budget.
func (e *Engine) reserveTaskLaneAttempt(ctx context.Context, run ExecutionRun, task TaskExecution) error {
	if run.ParentRunID == "" {
		return nil
	}
	e.operationMu.Lock()
	defer e.operationMu.Unlock()
	e.laneCommitMu.Lock()
	defer e.laneCommitMu.Unlock()
	parent, err := e.store.GetRun(ctx, run.ParentRunID)
	if err != nil {
		return err
	}
	original, exists := parent.Tasks[task.TaskID]
	if !exists || run.ParentBinding != taskLaneBinding(parent, original, run.BaseCommit) {
		return ErrRunConflict
	}
	if parent.TaskLanes[task.TaskID] != run.RunID || parent.State.IsTerminal() || parent.State == RunPaused {
		return fmt.Errorf("%w: task lane parent stopped or differs", ErrRunBlocked)
	}
	if err = e.ValidateRunGoal(ctx, parent); err != nil {
		return err
	}
	target := parent.SessionID
	if target == "" {
		target = parent.GoalID
	}
	goal, err := e.goalReader.GetActiveGoalContract(ctx, target)
	if err != nil {
		return err
	}
	if limits := goal.BudgetLimits; limits != nil {
		if limits.MaxTotalTokens != nil || limits.MaxCostUSD != nil || limits.MaxHandoffs > 0 {
			// Native adapters do not supply an enforceable upper bound for these
			// dimensions. Unknown usage cannot safely reserve the remaining budget.
			return fmt.Errorf("%w: task lanes cannot enforce token, cost or handoff ceilings", ErrRunBlocked)
		}
		if limits.MaxDuration > 0 && !parent.StartedAt.IsZero() && time.Since(parent.StartedAt) >= limits.MaxDuration {
			return fmt.Errorf("%w: parent wall-clock budget exhausted", ErrRunBlocked)
		}
		if limits.MaxModelCalls > 0 && parent.ParallelCallsReserved >= limits.MaxModelCalls {
			return fmt.Errorf("%w: parent model-call budget exhausted", ErrRunBlocked)
		}
		if limits.MaxRetries > 0 && run.BudgetConsumed.ModelCalls >= 1 && parent.ParallelRetriesReserved >= limits.MaxRetries {
			return fmt.Errorf("%w: parent retry budget exhausted", ErrRunBlocked)
		}
	}
	parent.ParallelCallsReserved++
	if run.BudgetConsumed.ModelCalls >= 1 {
		parent.ParallelRetriesReserved++
	}
	return e.persistLaneSnapshotLocked(ctx, &parent)
}

// Reconciliation does not claim a successful interrupted provider turn. Only a
// lane with no live native turn and no process-local executor can become paused;
// its preserved worktree and canonical task must be reviewed before a retry.
func (e *Engine) ReconcileTaskLane(ctx context.Context, id string, version int64) (*ExecutionRun, error) {
	e.operationMu.Lock()
	defer e.operationMu.Unlock()
	if _, active := e.activeRuns[id]; active {
		return nil, ErrRunBlocked
	}
	run, err := e.store.GetRun(ctx, id)
	if err != nil {
		return nil, err
	}
	task := run.Tasks[run.LaneTaskID]
	if run.ParentRunID == "" || run.Version != version || run.State != RunRunning || task.NativeTurn != nil {
		return nil, ErrRunConflict
	}
	task.State = TaskPaused
	task.LastFailureReason = "interrupted task lane requires reviewed recovery"
	task.UpdatedAt = time.Now().UTC()
	run.Tasks[task.TaskID] = task
	run.State = RunPaused
	run.CurrentPhase = PhasePaused
	if err = e.persistRun(ctx, &run); err != nil {
		return nil, err
	}
	return &run, nil
}

func (e *Engine) validateTaskLaneExecution(run ExecutionRun) error {
	if len(run.TaskLanes) > 0 {
		return fmt.Errorf("%w: parent has isolated task lanes; select the exact task lane", ErrRunBlocked)
	}
	return nil
}

// ValidateTaskLaneBinding is a read-only admission check for trusted surfaces.
// It accepts only an original lane whose immutable owning parent is still live.
func (e *Engine) ValidateTaskLaneBinding(ctx context.Context, id string) error {
	return e.validateTaskLaneBinding(ctx, id, false)
}

// ValidateCompletedTaskLaneBinding is ValidateTaskLaneBinding for a historical
// record of a finished lane: the parent may already be DONE_PENDING_VERIFICATION
// because its last lane completed. Cancelled, failed, paused or rebound
// parents still refuse, and every identity is checked exactly as before.
func (e *Engine) ValidateCompletedTaskLaneBinding(ctx context.Context, id string) error {
	return e.validateTaskLaneBinding(ctx, id, true)
}

func (e *Engine) validateTaskLaneBinding(ctx context.Context, id string, completedParent bool) error {
	e.laneCommitMu.Lock()
	defer e.laneCommitMu.Unlock()
	child, err := e.store.GetRun(ctx, id)
	if err != nil {
		return err
	}
	parent, err := e.store.GetRun(ctx, child.ParentRunID)
	if err != nil {
		return err
	}
	original, exists := parent.Tasks[child.LaneTaskID]
	root, rootErr := filepath.EvalSymlinks(e.cfg.ProjectRoot)
	parentEnded := parent.State.IsTerminal() && !(completedParent && parent.State == RunDonePendingVerification)
	if child.ParentRunID == "" || !exists || parentEnded || parent.State == RunPaused || parent.State == RunCancelling || parent.TaskLanes[child.LaneTaskID] != child.RunID || child.ParentBinding != taskLaneBinding(parent, original, child.BaseCommit) || rootErr != nil || root != parent.Provenance.RepoRoot || child.Provenance.RepoRoot != parent.Provenance.RepoRoot {
		return ErrRunConflict
	}
	if child.ProjectID != parent.ProjectID || child.SessionID != parent.SessionID || child.GoalID != parent.GoalID || child.GoalRevision != parent.GoalRevision || child.PlanID != parent.PlanID || child.PlanVersion != parent.PlanVersion || child.Mode != plan.ModeUltra || child.Delivery != DeliveryPreserveBranch || parent.Mode != plan.ModeUltra || parent.Delivery != DeliveryPreserveBranch {
		return ErrRunConflict
	}
	task, exists := child.Tasks[child.LaneTaskID]
	if !exists || task.TaskID != original.TaskID || task.CanonicalTaskID != original.CanonicalTaskID || task.AssignedHarness != original.AssignedHarness || task.AssignedModel != original.AssignedModel || task.Description != original.Description {
		return ErrRunConflict
	}
	return nil
}

func (e *Engine) controlTaskLanes(ctx context.Context, parent ExecutionRun, operation string) error {
	if parent.ParentRunID != "" || len(parent.TaskLanes) == 0 || operation == "resume" {
		return nil
	}
	var result error
	for _, id := range parent.TaskLanes {
		run, err := e.store.GetRun(ctx, id)
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		if run.ParentRunID != parent.RunID {
			result = errors.Join(result, ErrRunConflict)
			continue
		}
		if run.State.IsTerminal() || run.State == RunDonePendingVerification {
			continue
		}
		if operation == "pause" && run.State != RunRunning && run.State != RunNeedsApproval {
			continue
		}
		_, err = e.controlRunLocked(ctx, id, operation, run.Version)
		result = errors.Join(result, err)
	}
	return result
}

func (e *Engine) SetTaskLaneParent(ctx context.Context, id, base string) error {
	e.operationMu.Lock()
	defer e.operationMu.Unlock()
	e.laneCommitMu.Lock()
	defer e.laneCommitMu.Unlock()
	run, err := e.store.GetRun(ctx, id)
	if err != nil {
		return err
	}
	if run.Mode != plan.ModeUltra || run.ParentRunID != "" || base == "" {
		return ErrRunInvalid
	}
	if run.Delivery == DeliveryPreserveBranch && run.BaseCommit == base {
		return nil
	}
	if run.State != RunReady || len(run.TaskLanes) > 0 {
		return ErrRunConflict
	}
	run.Delivery = DeliveryPreserveBranch
	run.BaseCommit = base
	return e.persistLaneSnapshotLocked(ctx, &run)
}

// This lock covers both parent control and child settlement. A cancelled parent
// can never race a later child result into an accepted completion. Each child
// keeps its own native revision; the parent stores only honest summary receipts.
func (e *Engine) persistTaskLaneRun(ctx context.Context, run *ExecutionRun) (bool, error) {
	if run.ParentRunID == "" && len(run.TaskLanes) == 0 {
		return false, nil
	}
	e.laneCommitMu.Lock()
	defer e.laneCommitMu.Unlock()
	var parent ExecutionRun
	var err error
	if run.ParentRunID != "" {
		parent, err = e.store.GetRun(ctx, run.ParentRunID)
		if err != nil {
			return true, err
		}
		if parent.TaskLanes[run.LaneTaskID] != run.RunID {
			return true, ErrRunConflict
		}
		if parent.State != RunCancelled && parent.State != RunCancelling {
			original, exists := parent.Tasks[run.LaneTaskID]
			if !exists || run.ParentBinding != taskLaneBinding(parent, original, run.BaseCommit) {
				return true, ErrRunConflict
			}
		}
		if parent.State == RunCancelled || parent.State == RunCancelling {
			task := run.Tasks[run.LaneTaskID]
			task.State = TaskCancelled
			task.LastFailureReason = "owning parent cancelled"
			run.Tasks[task.TaskID] = task
			run.State = RunCancelled
			run.CurrentPhase = PhaseTerminated
		}
	}
	if err = e.store.UpdateRun(ctx, *run); err != nil {
		return true, err
	}
	run.Version++
	run.UpdatedAt = time.Now().UTC()
	if run.ParentRunID == "" {
		return true, nil
	}
	if parent.TaskLaneStates == nil {
		parent.TaskLaneStates = make(map[string]TaskExecutionState)
	}
	parent.BudgetConsumed = BudgetUsage{}
	all := len(parent.TaskLanes) == len(parent.Tasks)
	for taskID, id := range parent.TaskLanes {
		child, err := e.store.GetRun(ctx, id)
		if err != nil {
			return true, err
		}
		if child.ParentRunID != parent.RunID {
			return true, ErrRunConflict
		}
		task := child.Tasks[taskID]
		parent.TaskLaneStates[taskID] = task.State
		attempts, err := e.taskLaneAttempts(ctx, parent.RunID, taskID, child)
		if err != nil {
			return true, err
		}
		for _, attempt := range attempts {
			parent.BudgetConsumed.ModelCalls += attempt.BudgetConsumed.ModelCalls
			parent.BudgetConsumed.InputTokens += attempt.BudgetConsumed.InputTokens
			parent.BudgetConsumed.OutputTokens += attempt.BudgetConsumed.OutputTokens
		}
		if task.State != TaskCompletedPendingVerify {
			all = false
		}
	}
	if all && parent.State == RunRunning {
		parent.State = RunDonePendingVerification
		parent.CurrentPhase = PhaseCompletedPending
		now := time.Now().UTC()
		parent.EndedAt = &now
	}
	return true, e.store.UpdateRun(ctx, parent)
}

// maxTaskLaneReworks bounds the rework identities searched for one task; the
// Marshal rework limit is far lower.
const maxTaskLaneReworks = 16

// taskLaneAttempts returns every attempt lane of a task up to and including
// current, so the parent's budget keeps the calls of returned attempts.
func (e *Engine) taskLaneAttempts(ctx context.Context, parentID, taskID string, current ExecutionRun) ([]ExecutionRun, error) {
	for n := 0; n <= maxTaskLaneReworks; n++ {
		if taskLaneID(parentID, taskID, n) != current.RunID {
			continue
		}
		attempts := make([]ExecutionRun, 0, n+1)
		for k := 0; k < n; k++ {
			earlier, err := e.store.GetRun(ctx, taskLaneID(parentID, taskID, k))
			if errors.Is(err, ErrRunNotFound) {
				continue // refused before its lane opened; nothing ran
			}
			if err != nil {
				return nil, err
			}
			attempts = append(attempts, earlier)
		}
		return append(attempts, current), nil
	}
	return []ExecutionRun{current}, nil
}

func (e *Engine) persistLaneSnapshotLocked(ctx context.Context, run *ExecutionRun) error {
	if err := e.store.UpdateRun(ctx, *run); err != nil {
		return err
	}
	run.Version++
	run.UpdatedAt = time.Now().UTC()
	return nil
}

func privateTaskAssignment(assignments plan.AssignmentPlan, task plan.Task) (string, bool) {
	for _, assignment := range assignments.Assignments {
		if assignment.Independent {
			continue
		}
		for _, id := range assignment.Tasks {
			if id == task.ID && assignment.Harness != "" && (task.Role == "" || string(assignment.Role) == task.Role) {
				return executionHarnessName(assignment.Harness), true
			}
		}
	}
	return "", false
}

// Profiles retain their probed identity; executable workers use the registered
// governed harness name. Only the explicitly supported native aliases translate.
func executionHarnessName(name string) string {
	switch name {
	case "claude-code":
		return "claude"
	case "antigravity":
		return "agy"
	}
	return name
}

func producerAssignmentAllowed(assignment plan.Assignment) bool { return !assignment.Independent }
func (e *Engine) settleCancelledTaskLane(run ExecutionRun, task TaskExecution, lease *Lease) {
	if run.ParentRunID != "" && lease != nil {
		_ = e.leases.ReleaseLease(lease.LeaseID, lease.AgentID, nowUTC())
	}
}

// Called under operationMu: a parent remains executing until its own admitted
// children settle, even after a stop is durably recorded on the parent.
func (e *Engine) taskLanesExecuting(runID string) bool {
	parent, err := e.store.GetRun(context.Background(), runID)
	if err != nil {
		return false
	}
	for _, id := range parent.TaskLanes {
		if _, live := e.activeRuns[id]; live {
			return true
		}
	}
	return false
}

// unstartedTaskLane reports a lane whose task attempt never started.
func unstartedTaskLane(lane ExecutionRun, taskID string) bool {
	task, ok := lane.Tasks[taskID]
	return ok && lane.State == RunReady && task.State == TaskPending && task.Attempts == 0 && task.StartedAt == nil && task.WorktreePath == "" && task.ResultCommit == ""
}

// earlierTaskLaneAttempt reports whether lane is the identity of an attempt of
// taskID numbered below rework.
func earlierTaskLaneAttempt(parentID, taskID, lane string, rework int) bool {
	for k := rework - 1; k >= 0; k-- {
		if lane == taskLaneID(parentID, taskID, k) {
			return true
		}
	}
	return false
}

// orphanTaskLane reports a lane recorded as running or cancelling without a
// live native turn. The caller has already checked that no executor of this
// runtime owns it.
func orphanTaskLane(lane ExecutionRun, taskID string) bool {
	task, ok := lane.Tasks[taskID]
	return ok && (lane.State == RunRunning || lane.State == RunCancelling) && task.NativeTurn == nil
}

// pauseOrphanTaskLane pauses an interrupted lane as ReconcileTaskLane does; it
// claims no result and keeps the lane's worktree and evidence. The caller
// holds laneCommitMu, so the lane is written to the store directly.
func (e *Engine) pauseOrphanTaskLane(ctx context.Context, lane ExecutionRun, taskID string) error {
	task := lane.Tasks[taskID]
	if !task.State.IsTerminal() {
		// Failed, as an interrupted attempt is recorded elsewhere: the lane
		// no longer holds its scope against the attempt that supersedes it.
		task.State = TaskFailed
	}
	task.LastFailureReason = "interrupted task lane superseded by a later attempt"
	task.UpdatedAt = time.Now().UTC()
	lane.Tasks[taskID] = task
	lane.State = RunPaused
	lane.CurrentPhase = PhasePaused
	return e.store.UpdateRun(ctx, lane)
}

// supersedeUnstartedTaskLane cancels a never-started lane so a later attempt
// can open its own; nothing ran in it, so no evidence is discarded.
func (e *Engine) supersedeUnstartedTaskLane(ctx context.Context, lane ExecutionRun, taskID string) error {
	task := lane.Tasks[taskID]
	task.State = TaskCancelled
	task.LastFailureReason = "superseded before start by a later attempt"
	task.UpdatedAt = time.Now().UTC()
	lane.Tasks[taskID] = task
	now := time.Now().UTC()
	lane.State = RunCancelled
	lane.CurrentPhase = PhaseTerminated
	lane.EndedAt = &now
	return e.store.UpdateRun(ctx, lane)
}
