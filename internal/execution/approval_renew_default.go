//go:build governed

package execution

import "time"

// Community keeps an expired approval pending until the operator acts.
func (e *Engine) renewExpiredTaskApproval(ExecutionRun, *TaskExecution, time.Time) bool { return false }
