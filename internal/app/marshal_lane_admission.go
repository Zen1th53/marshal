package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
)

const laneAdmissionEscalation = "task lane admission refused"

func firstNonEmpty(first, fallback string) string {
	if first != "" {
		return first
	}
	return fallback
}

// isTaskLaneAdmissionRefusal reports a dispatch the execution engine refused
// while opening the task's lane, before any worker ran. The error may arrive
// wrapped or as text through a driver, so the sentinel text is matched too.
func isTaskLaneAdmissionRefusal(err error) bool {
	if err == nil {
		return false
	}
	text := err.Error()
	for _, refusal := range []string{
		"process 05: invalid Marshal task branch",
		"process 05: task does not match the approved Marshal run",
		"process 05: dispatch differs from the stored Marshal task",
		"process 05: dispatch checks differ from the stored Marshal task",
		"process 05: task worktree differs from the reserved Marshal worktree",
		"process 05: Marshal task differs from the approved Process 04 plan",
		"process 05: checks differ from the approved Process 04 plan",
	} {
		if strings.Contains(text, refusal) {
			return true
		}
	}
	if !strings.Contains(text, "task lane") {
		return false
	}
	return errors.Is(err, execution.ErrRunBlocked) || errors.Is(err, execution.ErrRunConflict) ||
		strings.Contains(text, execution.ErrRunBlocked.Error()) || strings.Contains(text, execution.ErrRunConflict.Error())
}

// laneRefusalRetrySafe reports whether a lane refusal may go away by
// itself: the engine blocked the lane (too many tasks running, or an earlier
// attempt still holding it), as opposed to a workspace that does not match
// the approved plan, which a retry cannot change.
func laneRefusalRetrySafe(err error) bool {
	return errors.Is(err, execution.ErrRunBlocked) || strings.Contains(err.Error(), execution.ErrRunBlocked.Error())
}

// orphanLaneRefusal marks the engine's refusal of an attempt whose lane is
// still recorded as running with no executor: a restart interrupted it.
const orphanLaneRefusal = "orphan task lane requires explicit reconciliation"

// isOrphanLaneRefusal reports a lane refusal caused by an interrupted
// earlier attempt of the task (Marshal was stopped while its worker ran).
func isOrphanLaneRefusal(err error) bool {
	return err != nil && strings.Contains(err.Error(), orphanLaneRefusal)
}

// laneAdmissionRetried holds the tasks whose lane refusal Marshal already
// retried by itself, keyed by repository, run and plan task, so it retries
// once and then pauses. An operator retry clears the entry (RetryTask).
var laneAdmissionRetried sync.Map

func (s *MarshalService) laneAdmissionKey(runID, taskID string) string {
	return s.Repository + "\x00" + runID + "\x00" + taskID
}

// forgetLaneAdmissionRetry lets the next refusal of the task be retried by
// itself once more, after the operator retried it.
func (s *MarshalService) forgetLaneAdmissionRetry(runID, taskID string) {
	laneAdmissionRetried.Delete(s.laneAdmissionKey(runID, taskID))
}

// escalateTaskLaneAdmission stops a task whose lane could not be opened. No
// worker ran, so it is not a return and spends no rework; a refusal that
// repeats on every attempt therefore cannot exhaust the rework limit.
//
// A retry-safe refusal is first retried once by itself, as the operator's
// Enter would (RetryTask): the task goes back to be dispatched, nothing is
// counted against it, and Activity says so. If it is refused again, or the
// refusal cannot go away by itself, the task is escalated once and the run
// pauses until the operator retries it.
func (s *MarshalService) escalateTaskLaneAdmission(ctx context.Context, runID string, run marshal.Run, rev int64, i int, cause error) error {
	machine := cause.Error()
	if len(machine) > 300 {
		machine = machine[:300]
	}
	machine = laneAdmissionEscalation + ": " + machine
	t := run.Tasks[i]
	taskID := t.PlanTaskID
	orphan := isOrphanLaneRefusal(cause)
	if orphan && s.process05CloseOrphanLane != nil {
		// A restart interrupted the task's attempt. Marshal closes that
		// attempt (it keeps its work and evidence) and starts the task
		// again in a fresh lane; nothing is counted against the task.
		closeErr := s.process05CloseOrphanLane(ctx, run, t)
		if closeErr == nil {
			return s.restartInterruptedLaneTask(ctx, runID, run, rev, i, machine)
		}
		machine += "; closing the interrupted attempt: " + closeErr.Error()
	}
	if laneRefusalRetrySafe(cause) && !orphan {
		if _, retried := laneAdmissionRetried.LoadOrStore(s.laneAdmissionKey(runID, taskID), true); !retried {
			return s.retryLaneAdmissionOnce(ctx, runID, run, rev, i, machine)
		}
	}
	run.Operation = nil
	if err := marshal.SetTaskState(&run.Tasks[i], marshal.Escalated); err != nil {
		return err
	}
	run.Tasks[i].EscalationReason = machine
	run.Tasks[i].EscalationCode = escalationCode(marshal.CodeLaneAdmission, machine)
	capacity := errors.Is(cause, execution.ErrRunBlocked) && strings.Contains(cause.Error(), "capacity exhausted")
	reason, summary := "A task couldn't start: its workspace didn't match the approved plan. Nothing ran.", firstNonEmpty(strings.TrimSpace(t.Title), taskID)+" couldn't start: its workspace didn't match the plan."
	if capacity {
		reason = "A task couldn't start because too many tasks were running. Nothing ran."
		summary = firstNonEmpty(strings.TrimSpace(t.Title), taskID) + " couldn't start yet: too many tasks were running."
	} else if orphan {
		reason = "A task's earlier attempt was interrupted, and Marshal couldn't close it. Nothing new ran."
		summary = firstNonEmpty(strings.TrimSpace(t.Title), taskID) + " couldn't start: its earlier attempt was interrupted and is still recorded as running."
	}
	pauseMarshal(&run, marshal.Pause{Reason: reason, Resolutions: []string{"Press Enter to try it again."}, Detail: machine, Code: marshal.CodeLaneAdmission, Action: marshal.PauseRetryTask, TaskID: taskID})
	if err := s.save(ctx, runID, run, rev); err != nil {
		return err
	}
	return s.record(ctx, runID, taskID, events.EventTypeMarshalEscalated, map[string]any{"reason": machine, "summary": summary, "counted": false})
}

// restartInterruptedLaneTask sends a task whose interrupted attempt Marshal
// just closed back to be dispatched in its next lane. No worker of this
// attempt handed anything in, so nothing is counted against the task; the
// closed attempt keeps its lane, worktree and evidence.
func (s *MarshalService) restartInterruptedLaneTask(ctx context.Context, runID string, run marshal.Run, rev int64, i int, machine string) error {
	t := &run.Tasks[i]
	run.Operation = nil
	retireInterruptedLane(t)
	if err := marshal.SetTaskState(t, marshal.Returned); err != nil {
		return err
	}
	run.State = marshal.Reviewing
	summary := firstNonEmpty(strings.TrimSpace(t.Title), t.PlanTaskID) + ": its earlier attempt was interrupted; Marshal closed it and will start the task again."
	if err := s.save(ctx, runID, run, rev); err != nil {
		return err
	}
	s.forgetLaneAdmissionRetry(runID, t.PlanTaskID)
	if s.RecordActivity != nil {
		s.RecordActivity(summary)
	}
	return s.record(ctx, runID, t.PlanTaskID, events.EventTypeMarshalTaskReturned, map[string]any{"reason": machine, "summary": summary, "counted": false, "retry": true, "interrupted_lane_closed": true})
}

// retryLaneAdmissionOnce sends a task whose lane was refused back to be
// dispatched again, once, without counting it: no worker ran.
func (s *MarshalService) retryLaneAdmissionOnce(ctx context.Context, runID string, run marshal.Run, rev int64, i int, machine string) error {
	t := &run.Tasks[i]
	run.Operation = nil
	if err := marshal.SetTaskState(t, marshal.Returned); err != nil {
		return err
	}
	run.State = marshal.Reviewing
	summary := firstNonEmpty(strings.TrimSpace(t.Title), t.PlanTaskID) + " couldn't start yet; Marshal is trying it once more."
	if err := s.save(ctx, runID, run, rev); err != nil {
		return err
	}
	if s.RecordActivity != nil {
		s.RecordActivity(summary)
	}
	return s.record(ctx, runID, t.PlanTaskID, events.EventTypeMarshalTaskReturned, map[string]any{"reason": machine, "summary": summary, "counted": false, "retry": true, "lane_admission_retry": true})
}

// admissionHolds keeps, per repository and run, the last launch the
// dispatcher held this round without a time to retry, so a run that cannot
// move says why instead of a generic stop.
var admissionHolds sync.Map

func (s *MarshalService) noteAdmissionHold(runID string, held []DispatchAdmission) {
	key := s.Repository + "\x00" + runID
	if len(held) == 0 {
		admissionHolds.Delete(key)
		return
	}
	admissionHolds.Store(key, held[0])
}

func (s *MarshalService) admissionHold(runID string) (DispatchAdmission, bool) {
	v, ok := admissionHolds.Load(s.Repository + "\x00" + runID)
	if !ok {
		return DispatchAdmission{}, false
	}
	return v.(DispatchAdmission), true
}

// AdmissionHold is the launch the dispatcher last held for runID without a
// time to retry, for the Board to say why the run waits.
func (s *MarshalService) AdmissionHold(runID string) (DispatchAdmission, bool) {
	if s == nil {
		return DispatchAdmission{}, false
	}
	return s.admissionHold(runID)
}

// waitAdmission blocks until until, when a held launch may be admitted.
func (s *MarshalService) waitAdmission(ctx context.Context, until time.Time) error {
	if s.admissionWait != nil {
		return s.admissionWait(ctx, until)
	}
	d := until.Sub(s.clock())
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// AdmissionHoldError is a run that cannot move because admission holds its
// remaining tasks; Admission says which and why.
type AdmissionHoldError struct {
	RunID     string
	Admission DispatchAdmission
}

func (e *AdmissionHoldError) Error() string {
	return "marshal run " + e.RunID + " cannot progress: " + e.Admission.Reason
}

// StopReason is the stop as the Board shows it.
func (e *AdmissionHoldError) StopReason() (string, string) {
	task := firstNonEmpty(e.Admission.TaskID, "A task")
	switch e.Admission.Code {
	case AdmissionQuotaUnknown:
		return task + " is waiting: " + e.Admission.Provider + "'s usage limit can't be read after its reset, so nothing new starts on it.", "Refresh the provider's usage, or approve a plan change that moves the task, then press Enter."
	case AdmissionCapacity:
		return task + " is waiting: every lane new work may use is taken.", "Answer the waiting approvals or wait for the host lane cap to lift, then press Enter."
	}
	return task + " is waiting: " + e.Admission.Reason + ".", "Press Enter to try again."
}
