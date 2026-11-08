package app

import (
	"context"
	"errors"
	"strings"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
)

const laneAdmissionEscalation = "task lane admission refused"

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

// escalateTaskLaneAdmission stops a task whose lane could not be opened. No
// worker ran, so it is not a return and spends no rework; a refusal that
// repeats on every attempt therefore cannot exhaust the rework limit. The task
// is escalated once and the run pauses until the operator retries it.
func (s *MarshalService) escalateTaskLaneAdmission(ctx context.Context, runID string, run marshal.Run, rev int64, i int, cause error) error {
	reason := cause.Error()
	if len(reason) > 300 {
		reason = reason[:300]
	}
	reason = laneAdmissionEscalation + ": " + reason
	taskID := run.Tasks[i].PlanTaskID
	run.Operation = nil
	run.Tasks[i].State = marshal.Escalated
	run.Tasks[i].EscalationReason = reason
	pauseMarshal(&run, reason, "resolve the lane refusal, then /marshal retry "+taskID+" and /marshal resume", "")
	if err := s.save(ctx, runID, run, rev); err != nil {
		return err
	}
	return s.record(ctx, runID, taskID, events.EventTypeMarshalEscalated, map[string]any{"reason": reason, "counted": false})
}
