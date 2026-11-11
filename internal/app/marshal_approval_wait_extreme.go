//go:build !governed

package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

type process05ApprovalWait struct{ run, task, approval string }

func (p *process05ApprovalWait) Error() string {
	return fmt.Sprintf("process 05: approval %s required; use /marshal approve-task %s, then /marshal resume", p.approval, p.approval)
}
func process05ApprovalRequired(run, task, id string) error {
	return &process05ApprovalWait{run, task, id}
}

type process05ApprovalExpired struct {
	approvalID string
	expiresAt  time.Time
}

func (e *process05ApprovalExpired) Error() string {
	if !e.expiresAt.IsZero() {
		return fmt.Sprintf("process 05: exact continuation approval denied, expired or invalidated (expired at %s)", e.expiresAt.UTC().Format("2006-01-02T15:04:05Z07:00"))
	}
	return "process 05: exact continuation approval denied, expired or invalidated (expired)"
}

func isProcess05ApprovalExpired(err error, _ string) (*process05ApprovalExpired, bool) {
	if err == nil || errors.Is(err, execution.ErrRunConflict) {
		return nil, false
	}
	var expired *process05ApprovalExpired
	if errors.As(err, &expired) {
		return expired, true
	}

	return nil, false
}

func expiredApprovalReason(approvalID string, expiresAt time.Time) string {
	if !expiresAt.IsZero() {
		return fmt.Sprintf("approval %s expired at %s", approvalID, expiresAt.UTC().Format("2006-01-02T15:04:05Z07:00"))
	}
	return fmt.Sprintf("approval %s expired", approvalID)
}

// isProcess05ApprovalWaitExpired reports a TASK_EXECUTE approval that expired
// unanswered. In Extreme it never stops the run: the task alone is reworked
// in a fresh lane that requests a fresh approval.
func isProcess05ApprovalWaitExpired(err error) bool {
	_, ok := isProcess05ApprovalExpired(err, "")
	return ok
}

// MarshalApprovalWaitBinding identifies an exact typed continuation wait.
// It is a presentation hint, not approval or permission to resume execution.
func MarshalApprovalWaitBinding(cause error) (executionRunID, taskID, approvalID string, waiting bool) {
	var wait *process05ApprovalWait
	if !errors.As(cause, &wait) || wait == nil || wait.run == "" || wait.task == "" || wait.approval == "" || !onlyMarshalApprovalWait(cause, wait) {
		return "", "", "", false
	}
	return wait.run, wait.task, wait.approval, true
}

func onlyMarshalApprovalWait(cause error, wait *process05ApprovalWait) bool {
	if cause == wait {
		return true
	}
	if joined, ok := cause.(interface{ Unwrap() []error }); ok {
		causes := joined.Unwrap()
		return len(causes) == 1 && onlyMarshalApprovalWait(causes[0], wait)
	}
	if wrapped, ok := cause.(interface{ Unwrap() error }); ok {
		return onlyMarshalApprovalWait(wrapped.Unwrap(), wait)
	}
	return false
}

func (r *Runtime) wirePrivateProcess05Wait(s *MarshalService) {
	s.process05WaitReceipt = func(ctx context.Context, run marshal.Run, task marshal.Task, executionID, approvalID string) (bool, error) {
		service := r.Execution()
		actual, err := service.GetRun(ctx, executionID)
		if err != nil {
			return false, err
		}
		if !run.Process05Bound || actual.PlanID != run.PlanID || actual.PlanVersion != run.PlanVersion || actual.ProjectID != s.CanonicalPlanProjectID() || actual.Delivery != execution.DeliveryPreserveBranch {
			return false, execution.ErrRunConflict
		}
		if actual.ParentRunID != "" {
			if err := service.Engine().ValidateTaskLaneBinding(ctx, executionID); err != nil {
				return false, err
			}
		}
		value, exists := actual.Tasks[task.PlanTaskID]
		if !exists || value.TaskID != task.PlanTaskID || value.AssignedHarness != task.Worker || !process05ApprovalWaitBaseBound(actual, run, task, value) {
			return false, execution.ErrRunConflict
		}
		if err := service.Engine().ValidateRunGoal(ctx, actual); err != nil {
			return false, err
		}
		approval, err := service.Engine().ApprovalManager().GetApproval(approvalID)
		if err != nil {
			return false, err
		}
		if approval.RunID != executionID || approval.TaskID != task.PlanTaskID || approval.PlanID != run.PlanID || approval.PlanVersion != run.PlanVersion {
			return false, execution.ErrRunConflict
		}
		if (approval.Status == execution.ApprovalRequested || approval.Status == execution.ApprovalExpired) && (actual.State != execution.RunNeedsApproval || value.State != execution.TaskNeedsApproval || value.ApprovalID != approvalID) {
			return false, execution.ErrRunConflict
		}
		if approval.Status == execution.ApprovalRequested && approval.ExpiresAt != nil && !s.clock().Before(*approval.ExpiresAt) {
			return false, &process05ApprovalExpired{approvalID: approvalID, expiresAt: *approval.ExpiresAt}
		}
		switch approval.Status {
		case execution.ApprovalRequested:
			if actual.State != execution.RunNeedsApproval || value.State != execution.TaskNeedsApproval || value.ApprovalID != approvalID {
				return false, execution.ErrRunConflict
			}
			return true, nil
		case execution.ApprovalApproved, execution.ApprovalConsumed:
			if value.State == execution.TaskBlocked || value.State == execution.TaskFailed || value.State == execution.TaskCancelled {
				return false, execution.ErrRunConflict
			}
			return false, nil
		case execution.ApprovalExpired:
			var exp time.Time
			if approval.ExpiresAt != nil {
				exp = *approval.ExpiresAt
			}
			return false, &process05ApprovalExpired{approvalID: approvalID, expiresAt: exp}
		default:
			return false, fmt.Errorf("process 05: exact continuation approval denied, expired or invalidated")
		}
	}
	s.process05WaitPause = func(ctx context.Context, executionID string) error {
		service := r.Execution()
		run, err := service.GetRun(ctx, executionID)
		if err != nil {
			return err
		}
		// Retire only an isolated task lane; never terminate a shared run.
		if run.ParentRunID == "" || len(run.Tasks) != 1 {
			return execution.ErrRunConflict
		}
		if run.State == execution.RunNeedsApproval || run.State == execution.RunRunning || run.State == execution.RunPaused {
			now := time.Now().UTC()
			for taskID, task := range run.Tasks {
				if task.State == execution.TaskNeedsApproval || task.State == execution.TaskRunning || task.State == execution.TaskPending {
					task.State = execution.TaskFailed
					task.LastFailureReason = "process 05 approval expired"
					task.UpdatedAt = now
					run.Tasks[taskID] = task
				}
			}
			run.State = execution.RunFailed
			run.CurrentPhase = execution.PhaseTerminated
			run.EndedAt = &now
			return service.Engine().RunStore().UpdateRun(ctx, run)
		}
		return nil
	}
}

func process05ApprovalWaitBaseBound(actual execution.ExecutionRun, run marshal.Run, task marshal.Task, value execution.TaskExecution) bool {
	// Serial execution retains the initial delivery base on its shared run;
	// ExecuteTaskBound records the admitted dependency-integrated base on
	// each task. Isolated lanes own that task base at both levels. Preserve
	// both bindings rather than comparing a serial parent to a later task.
	deliveryBase := run.BaseCommit
	if actual.ParentRunID != "" {
		deliveryBase = task.BaseCommit
	}
	return deliveryBase != "" && actual.BaseCommit == deliveryBase && task.BaseCommit != "" && value.BaseCommit == task.BaseCommit
}

func (s *MarshalService) pausePrivateProcess05Approval(ctx context.Context, runID string, dispatch MarshalDispatch, cause error) (bool, error) {
	var wait *process05ApprovalWait
	if !errors.As(cause, &wait) {
		return false, nil
	}
	run, rev, err := s.load(context.WithoutCancel(ctx), runID)
	if err != nil {
		return true, err
	}
	i := taskIndex(run, dispatch.TaskID)
	if i < 0 || wait.task != dispatch.TaskID || run.Tasks[i].State != marshal.Dispatched || s.process05WaitReceipt == nil {
		return true, execution.ErrRunConflict
	}
	pending, err := s.process05WaitReceipt(ctx, run, run.Tasks[i], wait.run, wait.approval)
	if err != nil {
		if expired, ok := isProcess05ApprovalExpired(err, wait.approval); ok && reworkFailedLaneWorker(run, run.Tasks[i]) {
			// The approval can expire before the orchestration handle parks it.
			run.Tasks[i].Process05WaitRunID, run.Tasks[i].Process05WaitApprovalID = wait.run, wait.approval
			run.Tasks[i].State = marshal.Queued
			if err := s.save(context.WithoutCancel(ctx), runID, run, rev); err != nil {
				return true, err
			}
			return true, s.reworkExpiredProcess05Approval(ctx, run, run.Tasks[i], expired)
		}
		return true, err
	}
	// A decided approval (pending false) can arrive before the wait is
	// recorded: the Marshal answers as soon as the lane asks. That is not a
	// conflict; the task is parked with its exact binding and continues on
	// the next round, as a wait decided later would.
	run.Tasks[i].Process05WaitRunID, run.Tasks[i].Process05WaitApprovalID = wait.run, wait.approval
	// Only the orchestration handle ended. The actual lane/native turn and
	// its approval remain untouched; waiting is neither failure nor rework.
	run.Tasks[i].State = marshal.Queued
	// Isolated ULTRA lanes do not share a base, so one lane's pending
	// approval does not hold the others: Execute keeps dispatching and
	// reviewing them, and pauses only when nothing but waits remain.
	if pending && !parallelProcess05Waits(s.Gate, run) {
		pauseMarshal(&run, marshal.Pause{Reason: approvalWaitReason(1), Resolutions: []string{process05WaitResolution}, Detail: wait.Error(), ResumeState: marshal.Dispatching, Code: marshal.CodeApprovalWait, Action: marshal.PauseResume})
	}
	return true, s.save(context.WithoutCancel(ctx), runID, run, rev)
}

const process05WaitResolution = "When the Marshal has decided, press Enter to continue."

func approvalWaitReason(count int) string {
	if count == 1 {
		return "A task is waiting for the Marshal's go-ahead."
	}
	return fmt.Sprintf("%d tasks are waiting for the Marshal's go-ahead.", count)
}

// parallelProcess05Waits reports whether an approved ULTRA plan runs its
// governed tasks in isolated lanes, where an approval wait is per task.
func parallelProcess05Waits(gate marshal.CapabilityGate, run marshal.Run) bool {
	return run.Process05Bound && !serialProcess05Base(gate, run)
}

// privateProcess05WaitPending reports whether a parallel task still waits for
// its exact approval, so dispatch passes it by instead of relaunching it. A
// decided approval lets the task continue in its own lane. An expired
// approval returns only that task to rework, clearing its wait and leaving it
// queued for fresh approval on its next dispatch.
func (s *MarshalService) privateProcess05WaitPending(ctx context.Context, run marshal.Run, task marshal.Task) (bool, error) {
	if task.Process05WaitApprovalID == "" || task.State != marshal.Queued || !parallelProcess05Waits(s.Gate, run) {
		return false, nil
	}
	if s.process05WaitReceipt == nil {
		return false, execution.ErrRunConflict
	}
	waiting, err := s.process05WaitReceipt(ctx, run, task, task.Process05WaitRunID, task.Process05WaitApprovalID)
	if err != nil {
		if expired, ok := isProcess05ApprovalExpired(err, task.Process05WaitApprovalID); ok && reworkFailedLaneWorker(run, task) {
			if reworkErr := s.reworkExpiredProcess05Approval(ctx, run, task, expired); reworkErr != nil {
				return false, reworkErr
			}
			return false, nil
		}
		if isProcess05ApprovalWaitExpired(err) {
			// Passed by this round; clearExpiredProcess05Waits clears the
			// wait so the task's lane requests a fresh approval.
			return true, nil
		}
		return false, err
	}
	return waiting, nil
}

func (s *MarshalService) findRunIDForTask(ctx context.Context, run marshal.Run, task marshal.Task) string {
	parts := strings.Split(task.Branch, "/")
	if len(parts) >= 3 {
		if _, _, err := s.load(ctx, parts[1]); err == nil {
			return parts[1]
		}
	}
	if len(parts) == 2 {
		if _, _, err := s.load(ctx, parts[0]); err == nil {
			return parts[0]
		}
	}

	return ""
}

func (s *MarshalService) reworkExpiredProcess05Approval(ctx context.Context, run marshal.Run, task marshal.Task, expired *process05ApprovalExpired) error {
	runID := s.findRunIDForTask(ctx, run, task)
	if runID == "" {
		return fmt.Errorf("marshal run for task %s not found", task.PlanTaskID)
	}
	currentRun, rev, err := s.load(ctx, runID)
	if err != nil {
		return err
	}
	i := taskIndex(currentRun, task.PlanTaskID)
	if i < 0 {
		return model.ErrNotFound
	}
	if currentRun.PlanID != run.PlanID || currentRun.PlanVersion != run.PlanVersion || currentRun.Tasks[i].Branch != task.Branch {
		return execution.ErrRunConflict
	}
	t := &currentRun.Tasks[i]
	if t.Process05WaitApprovalID != task.Process05WaitApprovalID || t.Process05WaitRunID != task.Process05WaitRunID || t.State != marshal.Queued {
		return execution.ErrRunConflict
	}

	attempt := 1 + t.EvidenceAttemptBase + taskReturns(*t)
	if s.process05WaitPause != nil && t.Process05WaitRunID != "" {
		if err := s.process05WaitPause(ctx, t.Process05WaitRunID); err != nil {
			return err
		}
	}
	t.Process05WaitRunID = ""
	t.Process05WaitApprovalID = ""

	// An expired approval does not count toward the worker rework limit
	// because no worker ran for this attempt. We advance LaneReworkBase
	// so the next dispatch opens a fresh task lane and requests a fresh approval,
	// without penalizing the worker's rework allowance.
	t.LaneReworkBase++
	t.State = marshal.Queued

	reason := expiredApprovalReason(expired.approvalID, expired.expiresAt)
	review := marshal.Review{
		Reviewer: "marshal-runtime",
		Reasons:  []string{reason},
		Verdict:  marshal.VerdictReturn,
	}
	if _, err := s.Store.GetMarshalReview(ctx, runID, t.PlanTaskID, attempt); errors.Is(err, model.ErrNotFound) {
		if _, err = s.Store.SetMarshalReview(ctx, runID, t.PlanTaskID, attempt, review); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	eventData := map[string]any{
		"reason":           reason,
		"return_attempt":   fmt.Sprint(attempt),
		"return_reasons":   []string{reason},
		"expired_approval": expired.approvalID,
	}
	if !expired.expiresAt.IsZero() {
		eventData["expires_at"] = expired.expiresAt.UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	if err := s.record(ctx, runID, t.PlanTaskID, events.EventTypeMarshalTaskReturned, eventData); err != nil {
		return err
	}
	if err := s.save(ctx, runID, currentRun, rev); err != nil {
		return err
	}
	_, err = s.Charge(ctx, runID, t.PlanTaskID, "return", zeroMarshalCharge())
	return err
}

// pausePrivateProcess05Waits pauses a parallel run once nothing is left to
// move but tasks waiting for their exact approvals. It reports whether
// Execute should take another round: after the pause, or when a wait was
// decided since dispatch passed it by.
func (s *MarshalService) pausePrivateProcess05Waits(ctx context.Context, runID string) (bool, error) {
	run, rev, err := s.load(ctx, runID)
	if err != nil || !parallelProcess05Waits(s.Gate, run) || run.State == marshal.AwaitingUser {
		return false, err
	}
	if cleared, err := s.clearExpiredProcess05Waits(ctx, runID, run, rev); cleared || err != nil {
		return cleared, err
	}
	var details []string
	for _, task := range run.Tasks {
		if task.Process05WaitApprovalID == "" || task.State != marshal.Queued {
			continue
		}
		pending, err := s.privateProcess05WaitPending(ctx, run, task)
		if err != nil {
			return true, err
		}
		if !pending {
			return true, nil // decided: the next round's dispatch continues it
		}
		details = append(details, (&process05ApprovalWait{task.Process05WaitRunID, task.PlanTaskID, task.Process05WaitApprovalID}).Error())
	}
	if len(details) == 0 {
		return false, nil
	}
	pauseMarshal(&run, marshal.Pause{Reason: approvalWaitReason(len(details)), Resolutions: []string{process05WaitResolution}, Detail: strings.Join(details, "; "), ResumeState: marshal.Dispatching, Code: marshal.CodeApprovalWait, Action: marshal.PauseResume})
	return true, s.save(context.WithoutCancel(ctx), runID, run, rev)
}

// checkPrivateProcess05Approval reports whether resume must keep waiting. A
// serial run waits for its one task. A parallel run resumes once any wait is
// decided; tasks still pending are passed by at dispatch.
func (s *MarshalService) checkPrivateProcess05Approval(ctx context.Context, run marshal.Run) (bool, error) {
	pending, decided := false, false
	for _, task := range run.Tasks {
		if task.Process05WaitApprovalID == "" || task.State != marshal.Queued {
			continue
		}
		if s.process05WaitReceipt == nil {
			return false, execution.ErrRunConflict
		}
		waiting, err := s.process05WaitReceipt(ctx, run, task, task.Process05WaitRunID, task.Process05WaitApprovalID)
		if isProcess05ApprovalWaitExpired(err) && parallelProcess05Waits(s.Gate, run) {
			// An isolated ULTRA lane: resume continues, dispatch clears the
			// expired wait and the task's lane requests a fresh approval. A
			// serial run keeps its refusal.
			waiting, err = false, nil
		}
		if err != nil {
			if _, ok := isProcess05ApprovalExpired(err, task.Process05WaitApprovalID); ok && parallelProcess05Waits(s.Gate, run) && reworkFailedLaneWorker(run, task) {
				// Expired approval does not hold up parallel resume; the task will be reworked on next dispatch in Execute.
				decided = true
				continue
			}
			return false, err
		}
		pending, decided = pending || waiting, decided || !waiting
	}
	if parallelProcess05Waits(s.Gate, run) && decided {
		return false, nil
	}
	return pending, nil
}

// clearExpiredProcess05WaitsBeforeDispatch clears expired waits of a parallel
// run at the start of every dispatch round and reports whether it changed the
// run, so an expired task never waits for its siblings to finish.
func (s *MarshalService) clearExpiredProcess05WaitsBeforeDispatch(ctx context.Context, runID string) (bool, error) {
	run, rev, err := s.load(ctx, runID)
	if err != nil || !parallelProcess05Waits(s.Gate, run) || run.State == marshal.AwaitingUser {
		return false, err
	}
	return s.clearExpiredProcess05Waits(ctx, runID, run, rev)
}

// clearExpiredProcess05Waits handles every queued task whose approval
// expired unanswered and reports whether it changed the run. A governed task
// in an isolated lane is returned to rework alone: its lane is retired, the
// reason is recorded and its next dispatch opens a fresh lane that requests a
// fresh approval, with no worker rework charged. Any other expired wait is
// cleared so its lane re-enters and the engine renews the approval
// (APPROVAL_EXPIRED_RENEWED). Sibling tasks and the run are never paused.
func (s *MarshalService) clearExpiredProcess05Waits(ctx context.Context, runID string, run marshal.Run, rev int64) (bool, error) {
	if s.process05WaitReceipt == nil {
		return false, nil
	}
	reworked := false
	var cleared []string
	for _, task := range run.Tasks {
		if task.Process05WaitApprovalID == "" || task.State != marshal.Queued {
			continue
		}
		_, err := s.process05WaitReceipt(ctx, run, task, task.Process05WaitRunID, task.Process05WaitApprovalID)
		expired, ok := isProcess05ApprovalExpired(err, task.Process05WaitApprovalID)
		if !ok {
			continue
		}
		if reworkFailedLaneWorker(run, task) {
			if err := s.reworkExpiredProcess05Approval(ctx, run, task, expired); err != nil {
				return true, err
			}
			reworked = true
			continue
		}
		cleared = append(cleared, task.PlanTaskID)
	}
	if len(cleared) == 0 {
		return reworked, nil
	}
	if reworked {
		// The rework saved the run; clear the rest on the current revision.
		var err error
		if run, rev, err = s.load(ctx, runID); err != nil {
			return true, err
		}
	}
	for _, id := range cleared {
		if i := taskIndex(run, id); i >= 0 {
			run.Tasks[i].Process05WaitRunID, run.Tasks[i].Process05WaitApprovalID = "", ""
		}
	}
	return true, s.save(context.WithoutCancel(ctx), runID, run, rev)
}
