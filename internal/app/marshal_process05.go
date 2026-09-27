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
)

// marshalProcess05Run executes one Marshal task only when it is exactly the
// active Process 04 plan. Process 05 owns worker execution and its worktree;
// Marshal imports only the completed commit into its task branch.
func (r *Runtime) marshalProcess05Run(s *MarshalService) driver.GovernedRunner {
	return func(ctx context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		parts := strings.Split(req.Task.Branch, "/")
		if len(parts) != 3 || parts[0] != "marshal" || parts[2] != req.Task.PlanTaskID {
			return nil, errors.New("process 05: invalid Marshal task branch")
		}
		run, err := s.Snapshot(ctx, parts[1])
		if err != nil {
			return nil, err
		}
		if len(run.Tasks) == 0 {
			return nil, errors.New("process 05: task does not match the approved Marshal run")
		}
		storedIndex := taskIndex(run, req.Task.PlanTaskID)
		if storedIndex < 0 || run.Tasks[storedIndex].Mode != marshal.Governed {
			return nil, errors.New("process 05: task does not match the approved Marshal run")
		}
		stored := run.Tasks[storedIndex]
		if stored.Worker != req.Task.Worker || stored.Branch != req.Task.Branch || !sameStrings(stored.Files, req.Task.Files) || !sameStrings(stored.Criteria, req.Task.Criteria) || len(stored.Checks) != len(req.Task.Checks) {
			return nil, errors.New("process 05: dispatch differs from the stored Marshal task")
		}
		for i, check := range stored.Checks {
			if check.Command != req.Task.Checks[i].Command || !sameStrings(check.Criteria, req.Task.Checks[i].Criteria) {
				return nil, errors.New("process 05: dispatch checks differ from the stored Marshal task")
			}
		}
		expectedWorktree := filepath.Join(s.Worktrees, "TASK-"+parts[1]+"-"+req.Task.PlanTaskID)
		actual, err := filepath.EvalSymlinks(req.Worktree)
		if err != nil {
			return nil, err
		}
		expected, err := filepath.EvalSymlinks(expectedWorktree)
		if err != nil || actual != expected {
			return nil, errors.New("process 05: task worktree differs from the reserved Marshal worktree")
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
		if approvedTask == nil || !sameStrings(approvedTask.Paths, req.Task.Files) || !sameStrings(approvedTask.Criteria, req.Task.Criteria) || !sameStrings(approvedTask.DependsOn, req.Task.DependsOn) {
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
		service := r.Execution()
		if service == nil {
			return nil, errors.New("process 05: execution service is unavailable")
		}
		digest := fmt.Sprintf("%s/%s", p.Goal.RequestDigest, p.Goal.ConstraintDigest)
		p05, err := service.StartRunBound(ctx, goal.SessionID, planProjectID, p.ID, p.Version, digest)
		if err != nil {
			return nil, err
		}
		task, ok := p05.Tasks[req.Task.PlanTaskID]
		if !ok || task.AssignedHarness != req.Task.Worker {
			return nil, errors.New("process 05: governed harness differs from the Marshal worker")
		}
		if p05.State == execution.RunReady {
			if err := service.SetPreserveBranch(ctx, p05.RunID, run.BaseCommit); err != nil {
				return nil, err
			}
		} else if p05.Delivery != execution.DeliveryPreserveBranch || p05.BaseCommit != run.BaseCommit {
			return nil, errors.New("process 05: resumed run has a different branch binding")
		}
		completed, err := service.ExecuteTaskBound(ctx, p05.RunID, req.Task.PlanTaskID, req.Task.BaseCommit)
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
		if err != nil {
			return nil, err
		}
		result := completed.Tasks[req.Task.PlanTaskID]
		if completed.State == execution.RunNeedsApproval && result.ApprovalID != "" {
			return nil, fmt.Errorf("process 05: approval %s required; use /marshal approve-task %s, then /marshal resume", result.ApprovalID, result.ApprovalID)
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
		if _, err := gitMarshal(ctx, req.Worktree, "reset", "--hard", result.ResultCommit); err != nil {
			return nil, err
		}
		if head, err := gitMarshal(ctx, filepath.Clean(req.Worktree), "rev-parse", "HEAD"); err != nil || head != result.ResultCommit {
			return nil, errors.New("process 05: imported commit does not match the governed result")
		}
		return []marshal.CommandRecord{{Command: "process05 " + p05.RunID, Output: "imported " + result.ResultCommit}}, nil
	}
}
