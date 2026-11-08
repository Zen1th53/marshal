//go:build governed

package execution

import (
	"context"
	"testing"
	"time"
)

// Community keeps every approval an explicit decision: the approved-plan
// hooks never grant, and no decider name is reserved.
func TestCommunityApprovedPlanAutoGrantIsNoOp(t *testing.T) {
	e, err := NewEngine(EngineConfig{ProjectRoot: t.TempDir()}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	run := ExecutionRun{RunID: "run", PlanID: "plan", PlanVersion: 1, State: RunRunning, SelectedTask: "task"}
	task := TaskExecution{RunID: "run", TaskID: "task", Description: "d", TargetFiles: []string{"a.go"}, ApprovalRequired: true}
	app, err := e.approvals.RequestApproval(ApprovalRequest{RunID: "run", TaskID: "task", PlanID: "plan", PlanVersion: 1, OperationType: "TASK_EXECUTE", TargetResource: "a.go", RiskLevel: "R2", Scope: "task_mutation", Parameters: "d"})
	if err != nil {
		t.Fatal(err)
	}
	task.ApprovalID = app.ApprovalID
	if e.grantApprovedPlanTask(context.Background(), run, task, *app) || e.grantApprovedPlanTransport(context.Background(), run, task, app.ApprovalID, *app) {
		t.Fatal("Community auto-granted an approval")
	}
	after, err := e.approvals.GetApproval(app.ApprovalID)
	if err != nil || after.Status != ApprovalRequested || after.Revision != app.Revision {
		t.Fatalf("Community hook changed the approval: %+v %v", after, err)
	}
	if reservedApprovalDecider(context.Background(), "runtime:extreme-approved-plan") {
		t.Fatal("Community reserved a decider name")
	}
	if err := e.approvals.Approve(app.ApprovalID, "runtime:extreme-approved-plan", "operator", time.Now()); err != nil {
		t.Fatalf("Community decision path changed: %v", err)
	}
}
