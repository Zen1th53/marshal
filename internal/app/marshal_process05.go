package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/workerterminal"
)

// ErrProcess05Admission identifies a deterministic dispatch mismatch before
// provider execution. Orchestration can report it without spending infra retries.
var ErrProcess05Admission = errors.New("process 05: dispatch admission refused")

// ErrProcess05Paused is a dispatch that stopped because the person paused
// the execution run its task runs in before the task finished. It is not a
// failure: the task goes back to waiting, with no rework counted, and the
// next dispatch after resume continues the same lane (parkPausedTask).
var ErrProcess05Paused = errors.New("process 05: the run is paused")

// marshalProcess05Run executes one Marshal task only when it is exactly the
// active Process 04 plan. Process 05 owns worker execution and its worktree;
// Marshal imports only the completed commit into its task branch.
func (r *Runtime) marshalProcess05Run(s *MarshalService) driver.GovernedRunner {
	return func(ctx context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		if req.RunID == "" {
			return nil, fmt.Errorf("%w: missing Marshal run identity", ErrProcess05Admission)
		}
		run, err := s.Snapshot(ctx, req.RunID)
		if err != nil {
			return nil, err
		}
		if len(run.Tasks) == 0 {
			return nil, fmt.Errorf("%w: task does not match the approved Marshal run", ErrProcess05Admission)
		}
		storedIndex := taskIndex(run, req.Task.PlanTaskID)
		if storedIndex < 0 || run.Tasks[storedIndex].Mode != marshal.Governed {
			return nil, fmt.Errorf("%w: task does not match the approved Marshal run", ErrProcess05Admission)
		}
		stored := run.Tasks[storedIndex]
		if stored.EffectiveType() != req.Task.EffectiveType() || stored.Title != req.Task.Title || stored.Worker != req.Task.Worker || stored.Branch != req.Task.Branch || !sameStrings(stored.Files, req.Task.Files) || !sameStrings(stored.Criteria, req.Task.Criteria) || len(stored.Checks) != len(req.Task.Checks) {
			return nil, fmt.Errorf("%w: dispatch differs from the stored Marshal task", ErrProcess05Admission)
		}
		for i, check := range stored.Checks {
			if check.Command != req.Task.Checks[i].Command || !sameStrings(check.Criteria, req.Task.Checks[i].Criteria) {
				return nil, fmt.Errorf("%w: dispatch checks differ from the stored Marshal task", ErrProcess05Admission)
			}
		}
		expectedWorktree := filepath.Join(s.Worktrees, taskArtifactID(req.RunID, stored))
		actual, err := filepath.EvalSymlinks(req.Worktree)
		if err != nil {
			return nil, err
		}
		expected, err := filepath.EvalSymlinks(expectedWorktree)
		if err != nil || actual != expected {
			return nil, fmt.Errorf("%w: task worktree differs from the reserved Marshal worktree", ErrProcess05Admission)
		}
		p, err := s.Store.GetPlan(ctx, run.PlanID, run.PlanVersion)
		if err != nil {
			return nil, err
		}
		if p.State != plan.StateApproved || len(p.Tasks) != len(run.Tasks) {
			return nil, errors.New("process 05: Marshal task differs from the approved Process 04 plan")
		}
		var approvedTask *plan.Task
		for i := range p.Tasks {
			if p.Tasks[i].ID == req.Task.PlanTaskID {
				approvedTask = &p.Tasks[i]
			}
		}
		if approvedTask == nil || approvedTask.Title != req.Task.Title || !sameStrings(approvedTask.Paths, req.Task.Files) || !sameStrings(approvedTask.Criteria, req.Task.Criteria) || !sameStrings(approvedTask.DependsOn, req.Task.DependsOn) {
			return nil, errors.New("process 05: Marshal task differs from the approved Process 04 plan")
		}
		commands := make([]string, 0, len(req.Task.Checks))
		for _, check := range req.Task.Checks {
			commands = append(commands, check.Command)
		}
		if !sameStrings(p.Checks[req.Task.PlanTaskID], commands) {
			return nil, errors.New("process 05: checks differ from the approved Process 04 plan")
		}
		planProjectID := s.CanonicalPlanProjectID()
		active, err := s.Store.GetActivePlan(ctx, planProjectID)
		if err != nil || active.ID != p.ID || active.Version != p.Version {
			return nil, errors.New("process 05: approved plan is no longer active")
		}
		goal, err := s.Store.GetGoalContract(ctx, p.Goal.GoalID, p.Goal.Revision)
		if err != nil {
			return nil, err
		}
		activeGoal, err := s.Store.GetActiveGoalContract(ctx, goal.SessionID)
		if err != nil || activeGoal.ID != goal.ID || activeGoal.Revision != goal.Revision || !goal.Confirmation.Settled() {
			return nil, errors.New("process 05: approved goal is no longer active")
		}
		// The approved plan note and earlier return reasons travel inside the
		// provider context; the private plan pack is never in the worktree.
		brief, err := r.process05Brief(ctx, s, req.RunID, run, stored)
		if err != nil {
			return nil, err
		}
		service := r.Execution()
		if service == nil {
			return nil, errors.New("process 05: execution service is unavailable")
		}
		// The Process 04 -> 05 handoff is the durable evidence that this
		// exact approved plan and Goal revision were handed to execution;
		// review provenance later requires it. Recording it is idempotent.
		if _, err := r.Plans().Handoff(ctx, goal.SessionID, planProjectID); err != nil {
			return nil, fmt.Errorf("process 05: plan handoff: %w", err)
		}
		digest := fmt.Sprintf("%s/%s", p.Goal.RequestDigest, p.Goal.ConstraintDigest)
		p05, err := service.StartRunBound(ctx, goal.SessionID, planProjectID, p.ID, p.Version, digest)
		if err != nil {
			return nil, err
		}
		// An operator's provider switch rebinds the task before its lane opens.
		p05, err = r.prepareProcess05ProviderSwitch(ctx, s, run, req, p05)
		if err != nil {
			return nil, err
		}
		// A worker the approved plan does not assign never opens a lane: the
		// mismatch is refused before any attempt exists. A provider failover
		// rebinds its own durable decision below.
		if task, ok := p05.Tasks[req.Task.PlanTaskID]; ok && !providerFailoverPending(req.Task) && task.AssignedHarness != req.Task.Worker {
			return nil, errGovernedHarnessDiffers
		}
		p05, err = r.selectProcess05TaskLane(ctx, s, run, req, p05)
		if err != nil {
			return nil, err
		}
		p05, err = r.prepareProcess05ProviderFailover(ctx, s, run, req, p05)
		if err != nil {
			return nil, err
		}
		task, ok := p05.Tasks[req.Task.PlanTaskID]
		if !ok || task.AssignedHarness != req.Task.Worker {
			return nil, errGovernedHarnessDiffers
		}
		if err := workerterminal.ReportIdentity(ctx, workerterminal.Identity{ExecutionRunID: p05.RunID, CanonicalTaskID: task.CanonicalTaskID}); err != nil {
			return nil, err
		}
		executionBase := run.BaseCommit
		if p05.ParentRunID != "" {
			executionBase = req.Task.BaseCommit
		}
		if p05.State == execution.RunReady {
			if err := service.SetPreserveBranch(ctx, p05.RunID, executionBase); err != nil {
				return nil, err
			}
		} else if p05.Delivery != execution.DeliveryPreserveBranch || p05.BaseCommit != executionBase {
			return nil, errors.New("process 05: resumed run has a different branch binding")
		}
		// Extreme binds this dispatch to its approved plan; Community: no-op.
		completed, err := service.ExecuteTaskBound(withProcess05Brief(withWorkerStall(r.privateApprovedPlanExecution(ctx, run, p05, req.Task.PlanTaskID), run.Settings), req.Task.PlanTaskID, brief), p05.RunID, req.Task.PlanTaskID, req.Task.BaseCommit)
		if errors.Is(err, execution.ErrInvalidStateTransition) && strings.Contains(err.Error(), "already executing") {
			// A provider-native approval resumes its bound turn in the background.
			// The Marshal resume command waits for that same task's durable result.
			ticker := time.NewTicker(250 * time.Millisecond)
			defer ticker.Stop()
			for {
				observed, getErr := service.GetRun(ctx, p05.RunID)
				completed, err = &observed, getErr
				if err != nil || completed.State == execution.RunPaused || completed.State == execution.RunDonePendingVerification || completed.State == execution.RunNeedsApproval || completed.State.IsTerminal() {
					break
				}
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-ticker.C:
				}
			}
		}
		if err == nil && completed != nil && completed.State == execution.RunPaused {
			if state := completed.Tasks[req.Task.PlanTaskID].State; state != execution.TaskCompletedPendingVerify && !state.IsTerminal() {
				return nil, ErrProcess05Paused
			}
		}
		if err = r.process05ProviderFailure(ctx, req, completed, err); err != nil {
			return nil, err
		}
		result := completed.Tasks[req.Task.PlanTaskID]
		r.honeypotMu.Lock()
		trap := r.honeypots[result.WorktreePath]
		r.honeypotMu.Unlock()
		if result.State == execution.TaskCompletedPendingVerify && trap == nil {
			return nil, errors.New("honeypot: required scan identity unavailable after recovery")
		}
		if trap != nil {
			if err := r.checkHoneypot(ctx, req.Task.PlanTaskID, trap, nil, nil, EgressAlert{RunID: p05.RunID, TaskID: result.CanonicalTaskID, Worker: req.Task.Worker}); err != nil {
				return nil, err
			}
		}
		if completed.State == execution.RunNeedsApproval && result.ApprovalID != "" {
			return nil, process05ApprovalRequired(completed.RunID, req.Task.PlanTaskID, result.ApprovalID)
		}
		if (completed.State != execution.RunDonePendingVerification && completed.State != execution.RunPaused) || result.State != execution.TaskCompletedPendingVerify || result.ResultCommit == "" || result.WorktreePath == "" {
			return nil, fmt.Errorf("process 05: task did not complete (run=%s task=%s)", completed.State, result.State)
		}
		if head, err := gitMarshal(ctx, result.WorktreePath, "rev-parse", "HEAD"); err != nil || head != result.ResultCommit {
			return nil, errors.New("process 05: result commit differs from its worktree")
		}
		if _, err := gitMarshal(ctx, result.WorktreePath, "merge-base", "--is-ancestor", req.Task.BaseCommit, result.ResultCommit); err != nil {
			return nil, errors.New("process 05: result is not based on the approved commit")
		}
		if head, err := gitMarshal(ctx, req.Worktree, "rev-parse", "HEAD"); err != nil || head != req.Task.BaseCommit {
			return nil, errors.New("process 05: Marshal task branch moved before import")
		}
		if status, err := gitMarshal(ctx, req.Worktree, "status", "--porcelain"); err != nil || status != "" {
			return nil, errors.New("process 05: Marshal task worktree changed before import")
		}
		observations, err := marshalProcess05Observations(completed, result)
		if err != nil {
			return nil, err
		}
		provenance, err := r.marshalProcess05Provenance(ctx, run, completed, result)
		if err != nil {
			return nil, err
		}
		r.honeypotMu.Lock()
		if trap != nil {
			r.honeypots[req.Worktree] = trap
		}
		r.honeypotMu.Unlock()
		if _, err := gitMarshal(ctx, req.Worktree, "reset", "--hard", result.ResultCommit); err != nil {
			return nil, err
		}
		if head, err := gitMarshal(ctx, filepath.Clean(req.Worktree), "rev-parse", "HEAD"); err != nil || head != result.ResultCommit {
			return nil, errors.New("process 05: imported commit does not match the governed result")
		}
		r.honeypotMu.Lock()
		r.honeypots[req.Worktree] = trap
		r.honeypotMu.Unlock()
		return marshalProcess05ReviewRecords(ctx, p05.RunID, result.ResultCommit, observations, provenance)
	}
}

// Publication stays inside the single-use governed callback. The driver keeps
// these runtime records separate from any similarly named worker output.
func marshalProcess05ReviewRecords(ctx context.Context, lane, result string, observations, provenance []marshal.CommandRecord) ([]marshal.CommandRecord, error) {
	records := append([]marshal.CommandRecord{{Command: "process05 " + lane, Output: "imported " + result}}, observations...)
	records = append(records, provenance...)
	if len(observations) > 0 || len(provenance) > 0 {
		if err := driver.ReportRuntimeObserved(ctx, records); err != nil {
			return nil, err
		}
	}
	return records, nil
}
