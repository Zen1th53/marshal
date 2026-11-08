//go:build !governed

package execution

import (
	"context"
	"testing"
)

// TestExtremeApprovedPlanGrantsRecordedProviderSwitch: owner rule (Extreme):
// a task the operator reassigned inside the approved plan does not wait for a
// task approval. The lane parent's durable provider-switch record stands in
// for the plan's original assignment. A provider change without that record
// still waits (TestExtremeApprovedPlanTaskApprovalOutsideScopeWaits,
// provider-switched).
func TestExtremeApprovedPlanGrantsRecordedProviderSwitch(t *testing.T) {
	e, first, base := approvedPlanLane(t, coveredGate())
	calls := 0
	e.RegisterHarness(NewMockHarness("mock2", func(_ context.Context, task TaskExecution, _ ConstraintPackage, _ string) (TaskResult, error) {
		calls++
		return TaskResult{TaskID: task.TaskID, Success: true}, nil
	}))
	parent := mustRun(t, e, first.ParentRunID)
	from := parent.Tasks["task-1"].AssignedHarness
	allow := func(ExecutionRun, TaskExecution) error { return nil }
	if _, err := e.RebindTaskLaneProviderSwitch(t.Context(), parent.RunID, parent.Version, "task-1", from, "mock2", "model-2", "receipt-1", allow); err != nil {
		t.Fatal(err)
	}
	second, err := e.OpenTaskLaneRework(t.Context(), parent.RunID, "task-1", base, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !ProviderSwitchAssigns(mustRun(t, e, parent.RunID), second.Tasks["task-1"]) {
		t.Fatal("fixture: the switched lane task is not the recorded switch")
	}
	ctx := WithApprovedPlanExecution(t.Context(), approvedPlanBinding(second))
	done, err := e.ExecuteTaskBound(ctx, second.RunID, "task-1", base)
	if err != nil || done.State == RunNeedsApproval || done.Tasks["task-1"].State != TaskCompletedPendingVerify || calls != 1 {
		t.Fatalf("reassigned task waited for a task approval: %+v calls=%d err=%v", done, calls, err)
	}
	approval, err := e.approvals.GetApproval(done.Tasks["task-1"].ApprovalID)
	if err != nil || !IsApprovedPlanGrant(*approval) || approval.OperationType != "TASK_EXECUTE" {
		t.Fatalf("grant not recorded on the exact approval: %+v %v", approval, err)
	}
	if grantEvents(t, e, second.RunID) != 1 {
		t.Fatal("approved-plan grant event absent")
	}
}
