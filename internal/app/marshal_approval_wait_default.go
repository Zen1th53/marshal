//go:build governed

package app

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
)

func process05ApprovalRequired(_ string, _ string, id string) error {
	return fmt.Errorf("process 05: approval %s required; use /approve execution:%s, then /marshal resume", id, id)
}

// Community retains its historical approval error without a private wait binding.
func MarshalApprovalWaitBinding(error) (executionRunID, taskID, approvalID string, waiting bool) {
	return "", "", "", false
}
func (r *Runtime) wirePrivateProcess05Wait(*MarshalService) {}
func (s *MarshalService) pausePrivateProcess05Approval(context.Context, string, MarshalDispatch, error) (bool, error) {
	return false, nil
}
func (s *MarshalService) checkPrivateProcess05Approval(context.Context, marshal.Run) (bool, error) {
	return false, nil
}

// Community dispatches governed plan tasks one at a time; no task waits
// beside others for its own approval.
func (s *MarshalService) privateProcess05WaitPending(context.Context, marshal.Run, marshal.Task) (bool, error) {
	return false, nil
}
func (s *MarshalService) pausePrivateProcess05Waits(context.Context, string) (bool, error) {
	return false, nil
}
func (s *MarshalService) clearExpiredProcess05WaitsBeforeDispatch(context.Context, string) (bool, error) {
	return false, nil
}

// retryExpiredApprovalWait in the governed edition inspects the canonical
// Process 05 execution run directly rather than using private wait stubs.
// Explicit owner retry clears an expired unanswered approval binding without
// charging worker rework so the next dispatch creates a fresh ticket for the
// authenticated operator decision path.
func (s *MarshalService) retryExpiredApprovalWait(ctx context.Context, runID string, run marshal.Run, rev int64, taskIdx int) (bool, error) {
	if s.runtime == nil || s.runtime.Execution() == nil || s.runtime.Execution().Engine() == nil {
		return false, nil
	}
	t := &run.Tasks[taskIdx]
	if t.Mode != marshal.Governed || (t.State != marshal.Dispatched && t.State != marshal.Queued) {
		return false, nil
	}
	execRuns, err := s.runtime.Execution().ListRuns(ctx)
	if err != nil {
		return false, nil
	}
	planProject := s.CanonicalPlanProjectID()
	for _, p05 := range execRuns {
		if p05.PlanID != run.PlanID || p05.PlanVersion != run.PlanVersion || p05.ProjectID != planProject {
			continue
		}
		execTask, ok := p05.Tasks[t.PlanTaskID]
		if !ok || execTask.State != execution.TaskNeedsApproval || execTask.ApprovalID == "" {
			continue
		}
		cleared, clearErr := s.runtime.Execution().Engine().ClearExpiredTaskApproval(ctx, p05.RunID, t.PlanTaskID, s.clock())
		if clearErr != nil {
			return false, nil
		}
		reason := fmt.Sprintf("approval %s expired", cleared.ApprovalID)
		if cleared.ExpiresAt != nil {
			reason = fmt.Sprintf("approval %s expired at %s", cleared.ApprovalID, cleared.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z07:00"))
		}
		_ = s.runtime.RecordApprovalTerminal(ctx, runID, *cleared, "expired", reason)
		t.State = marshal.Queued
		if run.State == marshal.AwaitingUser {
			run.State = marshal.Dispatching
			run.Pause = nil
		}
		if err := s.save(ctx, runID, run, rev); err != nil {
			return true, err
		}
		eventData := map[string]any{
			"reason":           reason,
			"expired_approval": cleared.ApprovalID,
			"owner_retry":      true,
			"rework_charged":   false,
		}
		if cleared.ExpiresAt != nil {
			eventData["expires_at"] = cleared.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z07:00")
		}
		return true, s.record(ctx, runID, t.PlanTaskID, events.EventTypeMarshalTaskReturned, eventData)
	}
	return false, nil
}

// requeueGovernedApprovalTask reports whether a Dispatched governed task is
// waiting on (or just approved for) a Process 05 approval, so Resume returns
// it to Queued without charging a false worker rework return.
func (s *MarshalService) requeueGovernedApprovalTask(ctx context.Context, run marshal.Run, task marshal.Task) bool {
	if s.runtime == nil || s.runtime.Execution() == nil || task.Mode != marshal.Governed {
		return false
	}
	execRuns, err := s.runtime.Execution().ListRuns(ctx)
	if err != nil {
		return false
	}
	planProject := s.CanonicalPlanProjectID()
	for _, p05 := range execRuns {
		if p05.PlanID != run.PlanID || p05.PlanVersion != run.PlanVersion || p05.ProjectID != planProject {
			continue
		}
		execTask, ok := p05.Tasks[task.PlanTaskID]
		if ok && execTask.ApprovalID != "" && (execTask.State == execution.TaskNeedsApproval || execTask.State == execution.TaskReady) {
			return true
		}
	}
	return false
}
