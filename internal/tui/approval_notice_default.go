//go:build governed

package tui

func taskApprovalNotice(taskID, approvalID string) string {
	return "Task " + taskID + " is waiting for approval " + approvalID + "."
}
