//go:build !governed

package tui

// taskApprovalNotice names who settles a task's pending approval. In Extreme
// the Marshal decides it, so the line never reads as waiting for the owner.
func taskApprovalNotice(taskID, approvalID string) string {
	return "Task " + taskID + ": the Marshal is deciding approval " + approvalID + " (no owner action)."
}
