//go:build governed

package execution

import "context"

// Community keeps every approval an explicit operator decision.
func (*Engine) grantApprovedPlanTask(context.Context, ExecutionRun, TaskExecution, RuntimeApproval) bool {
	return false
}

func (*Engine) grantApprovedPlanTransport(context.Context, ExecutionRun, TaskExecution, string, RuntimeApproval) bool {
	return false
}

func reservedApprovalDecider(context.Context, string) bool { return false }
