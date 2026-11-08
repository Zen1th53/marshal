//go:build !governed

package execution

import (
	"fmt"
	"time"
)

// renewExpiredTaskApproval clears a task's TASK_EXECUTE approval that expired
// unanswered, so the same round requests a fresh one (owner rule: in Extreme
// an unanswered approval never stops the work). A decided approval, a denial
// and any other state keep their meaning; nothing is granted here.
func (e *Engine) renewExpiredTaskApproval(run ExecutionRun, t *TaskExecution, now time.Time) bool {
	if t.State != TaskNeedsApproval || t.ApprovalID == "" || !t.ApprovalRequired {
		return false
	}
	app, err := e.approvals.GetApproval(t.ApprovalID)
	if err != nil || app.OperationType != "TASK_EXECUTE" || app.RunID != run.RunID || app.TaskID != t.TaskID || app.PlanID != run.PlanID || app.PlanVersion != run.PlanVersion || app.ApprovedBy != "" || app.HardViolation {
		return false
	}
	expired := app.Status == ApprovalExpired || (app.Status == ApprovalRequested && app.ExpiresAt != nil && !now.Before(*app.ExpiresAt))
	if !expired {
		return false
	}
	_, _ = e.journal.Append(JournalEvent{
		RunID:       run.RunID,
		TaskID:      t.TaskID,
		Timestamp:   now,
		Actor:       "MARSHAL_ENGINE",
		EventType:   "APPROVAL_EXPIRED_RENEWED",
		StateBefore: string(TaskNeedsApproval),
		StateAfter:  string(TaskPending),
		Summary:     fmt.Sprintf("TASK_EXECUTE approval %s for task %s expired unanswered; a fresh approval is requested", app.ApprovalID, t.TaskID),
	})
	t.ApprovalID = ""
	t.State = TaskPending
	return true
}
