package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
)

// A stored native-turn run (from before the Claude stream and Codex app-server
// paths were removed) keeps its approval pending: approving or rejecting it is
// refused as unavailable, because no live provider turn can exist to release.
func TestStoredNativeTurnApprovalIsRefusedAndStaysPending(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct{ operation, provider string }{
		{claudeStreamApprovalOperation, "claude-stream"},
		{codexAppServerApprovalOperation, "codex-app-server"},
	} {
		t.Run(tc.operation, func(t *testing.T) {
			engine, err := execution.NewEngine(execution.EngineConfig{ProjectRoot: t.TempDir()}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			worktree := t.TempDir()
			record, err := engine.ApprovalManager().RequestApproval(execution.ApprovalRequest{
				RunID: "run-native", TaskID: "task-native", PlanID: "plan-native", PlanVersion: 1,
				OperationType: tc.operation, TargetResource: worktree, RiskLevel: model.R2,
				Scope: "native", DiffPreview: "method", Parameters: "sha256:native", CurrentState: "bound", Now: time.Now().UTC(),
			})
			if err != nil {
				t.Fatal(err)
			}
			run := execution.ExecutionRun{RunID: "run-native", Version: 1, PlanID: "plan-native", PlanVersion: 1, State: execution.RunNeedsApproval, Tasks: map[string]execution.TaskExecution{
				"task-native": {RunID: "run-native", TaskID: "task-native", State: execution.TaskNeedsApproval, ApprovalID: record.ApprovalID, NativeTurn: &execution.NativeTurnBinding{Provider: tc.provider, ThreadID: "thread", TurnID: "turn", Worktree: worktree, WorktreeDigest: "sha256:worktree", ConstraintDigest: "sha256:constraints", RunRevision: 1, State: "APPROVAL_REQUIRED"}},
			}}
			if err := engine.RunStore().CreateRun(ctx, run); err != nil {
				t.Fatal(err)
			}
			service := &ExecutionService{runtime: &Runtime{}, engine: engine, now: time.Now}
			if err := service.Approve(ctx, record.ApprovalID, "operator", "reviewed"); !errors.Is(err, model.ErrUnavailable) {
				t.Fatalf("approve stored native approval = %v, want model.ErrUnavailable", err)
			}
			if err := service.Reject(ctx, record.ApprovalID, "operator", "reviewed"); !errors.Is(err, model.ErrUnavailable) {
				t.Fatalf("reject stored native approval = %v, want model.ErrUnavailable", err)
			}
			// The engine's ordinary decision path refuses it as well.
			if err := engine.DecideApproval(ctx, record.ApprovalID, true, "operator", "approve"); !errors.Is(err, execution.ErrRunBlocked) {
				t.Fatalf("ordinary decision must not release a native approval: %v", err)
			}
			current, err := engine.ApprovalManager().GetApproval(record.ApprovalID)
			if err != nil || current.Status != execution.ApprovalRequested {
				t.Fatalf("refused native approval changed: %#v, %v", current, err)
			}
		})
	}
}
