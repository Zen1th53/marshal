package app

import (
	"context"
	"errors"
	"strings"
	"sync"

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
	if laneRefusalRetrySafe(cause) {
		if _, retried := laneAdmissionRetried.LoadOrStore(s.laneAdmissionKey(runID, taskID), true); !retried {
			return s.retryLaneAdmissionOnce(ctx, runID, run, rev, i, machine)
		}
	}
	run.Operation = nil
	run.Tasks[i].State = marshal.Escalated
	run.Tasks[i].EscalationReason = machine
	run.Tasks[i].EscalationCode = escalationCode(marshal.CodeLaneAdmission, machine)
	capacity := errors.Is(cause, execution.ErrRunBlocked) && strings.Contains(cause.Error(), "capacity exhausted")
	reason, summary := "A task couldn't start: its workspace didn't match the approved plan. Nothing ran.", firstNonEmpty(strings.TrimSpace(t.Title), taskID)+" couldn't start: its workspace didn't match the plan."
	if capacity {
		reason = "A task couldn't start because too many tasks were running. Nothing ran."
		summary = firstNonEmpty(strings.TrimSpace(t.Title), taskID) + " couldn't start yet: too many tasks were running."
	}
	pauseMarshal(&run, marshal.Pause{Reason: reason, Resolutions: []string{"Press Enter to try it again."}, Detail: machine, Code: marshal.CodeLaneAdmission, Action: marshal.PauseRetryTask, TaskID: taskID})
	if err := s.save(ctx, runID, run, rev); err != nil {
		return err
	}
	return s.record(ctx, runID, taskID, events.EventTypeMarshalEscalated, map[string]any{"reason": machine, "summary": summary, "counted": false})
}

// retryLaneAdmissionOnce sends a task whose lane was refused back to be
// dispatched again, once, without counting it: no worker ran.
func (s *MarshalService) retryLaneAdmissionOnce(ctx context.Context, runID string, run marshal.Run, rev int64, i int, machine string) error {
	t := &run.Tasks[i]
	run.Operation = nil
	t.State = marshal.Returned
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
