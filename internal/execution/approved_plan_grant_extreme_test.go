//go:build !governed

package execution

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/plan"
)

// approvedPlanLane opens an isolated lane for task-1 whose approved plan
// predicts the given approval gate, as an ULTRA Process 05 dispatch does.
func approvedPlanLane(t *testing.T, gate plan.ApprovalRequirement) (*Engine, *ExecutionRun, string) {
	t.Helper()
	e, parent, base := laneFixture(t)
	reader := e.planReader.(*MemoryPlanReader)
	p, err := reader.GetPlan(t.Context(), parent.PlanID, parent.PlanVersion)
	if err != nil {
		t.Fatal(err)
	}
	p.Approvals = []plan.ApprovalRequirement{gate}
	reader.AddPlan(p)
	lane, err := e.OpenTaskLane(t.Context(), parent.RunID, "task-1", base)
	if err != nil {
		t.Fatal(err)
	}
	task := lane.Tasks["task-1"]
	task.ApprovalRequired = true
	lane.Tasks["task-1"] = task
	if err := e.store.UpdateRun(t.Context(), *lane); err != nil {
		t.Fatal(err)
	}
	*lane, err = e.store.GetRun(t.Context(), lane.RunID)
	if err != nil {
		t.Fatal(err)
	}
	return e, lane, base
}

// coveredGate is what plan prediction attaches to every task of a broad goal.
func coveredGate() plan.ApprovalRequirement {
	return plan.ApprovalRequirement{Task: "task-1", Kind: plan.ApprovalPrivilege, Reason: "fixture", Hard: true}
}

func approvedPlanBinding(run *ExecutionRun) ApprovedPlanExecution {
	return ApprovedPlanExecution{MarshalRun: "MARSHAL-fixture", Run: run.RunID, Task: "task-1", Plan: run.PlanID, PlanVersion: run.PlanVersion}
}

func succeedingHarness(e *Engine, calls *int) {
	e.RegisterHarness(NewMockHarness("mock", func(_ context.Context, task TaskExecution, _ ConstraintPackage, _ string) (TaskResult, error) {
		*calls++
		return TaskResult{TaskID: task.TaskID, Success: true}, nil
	}))
}

func grantEvents(t *testing.T, e *Engine, runID string) int {
	t.Helper()
	events, err := e.journal.GetEvents(runID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range events {
		if event.EventType == ApprovedPlanEvent && event.Actor == ApprovedPlanDecider {
			count++
		}
	}
	return count
}

func TestExtremeApprovedPlanTaskApprovalRunsSameRound(t *testing.T) {
	e, lane, base := approvedPlanLane(t, coveredGate())
	calls := 0
	succeedingHarness(e, &calls)
	ctx := WithApprovedPlanExecution(t.Context(), approvedPlanBinding(lane))
	done, err := e.ExecuteTaskBound(ctx, lane.RunID, "task-1", base)
	if err != nil || done.State == RunNeedsApproval || done.Tasks["task-1"].State != TaskCompletedPendingVerify || calls != 1 {
		t.Fatalf("approved-plan task did not run in the same round: %+v calls=%d err=%v", done, calls, err)
	}
	approval, err := e.approvals.GetApproval(done.Tasks["task-1"].ApprovalID)
	if err != nil || approval.OperationType != "TASK_EXECUTE" || !IsApprovedPlanGrant(*approval) || approval.Status != ApprovalApproved || approval.TargetResource != "config.json" {
		t.Fatalf("runtime grant not recorded on the exact approval: %+v %v", approval, err)
	}
	if grantEvents(t, e, lane.RunID) != 1 {
		t.Fatal("approved-plan grant event absent")
	}
}

func TestExtremeApprovedPlanTaskApprovalOutsideScopeWaits(t *testing.T) {
	cases := map[string]struct {
		gate   plan.ApprovalRequirement
		ctx    func(context.Context, *ExecutionRun) context.Context
		change func(*TaskExecution)
	}{
		"unbound-run": {gate: coveredGate(), ctx: func(ctx context.Context, _ *ExecutionRun) context.Context { return ctx }},
		"foreign-run": {gate: coveredGate(), ctx: func(ctx context.Context, run *ExecutionRun) context.Context {
			b := approvedPlanBinding(run)
			b.Run = "RUN-foreign"
			return WithApprovedPlanExecution(ctx, b)
		}},
		"foreign-plan-version": {gate: coveredGate(), ctx: func(ctx context.Context, run *ExecutionRun) context.Context {
			b := approvedPlanBinding(run)
			b.PlanVersion++
			return WithApprovedPlanExecution(ctx, b)
		}},
		"foreign-task": {gate: coveredGate(), ctx: func(ctx context.Context, run *ExecutionRun) context.Context {
			b := approvedPlanBinding(run)
			b.Task = "task-2"
			return WithApprovedPlanExecution(ctx, b)
		}},
		"no-marshal-run": {gate: coveredGate(), ctx: func(ctx context.Context, run *ExecutionRun) context.Context {
			b := approvedPlanBinding(run)
			b.MarshalRun = ""
			return WithApprovedPlanExecution(ctx, b)
		}},
		"target-differs-from-plan": {gate: coveredGate(), change: func(task *TaskExecution) { task.TargetFiles = []string{"secrets.env"} }},
		"description-differs":      {gate: coveredGate(), change: func(task *TaskExecution) { task.Description = "something else" }},
		"secret-access":            {gate: plan.ApprovalRequirement{Task: "task-1", Kind: plan.ApprovalSecretAccess, Hard: true}},
		"network-access":           {gate: plan.ApprovalRequirement{Task: "task-1", Kind: plan.ApprovalNetwork}},
		"hard-destructive":         {gate: plan.ApprovalRequirement{Task: "task-1", Kind: plan.ApprovalDestructive, Hard: true}},
		"production":               {gate: plan.ApprovalRequirement{Task: "task-1", Kind: plan.ApprovalProduction, Hard: true}},
		"not-planned-approval":     {gate: plan.ApprovalRequirement{Task: "task-2", Kind: plan.ApprovalDestructive, Hard: true}},
		"provider-switched":        {gate: coveredGate(), change: func(task *TaskExecution) { task.AssignedModel = "other" }},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e, lane, base := approvedPlanLane(t, c.gate)
			if c.change != nil {
				task := lane.Tasks["task-1"]
				c.change(&task)
				lane.Tasks["task-1"] = task
				if err := e.store.UpdateRun(t.Context(), *lane); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			succeedingHarness(e, &calls)
			ctx := WithApprovedPlanExecution(t.Context(), approvedPlanBinding(lane))
			if c.ctx != nil {
				ctx = c.ctx(t.Context(), lane)
			}
			waiting, err := e.ExecuteTaskBound(ctx, lane.RunID, "task-1", base)
			if err != nil || waiting.State != RunNeedsApproval || waiting.Tasks["task-1"].State != TaskNeedsApproval || calls != 0 {
				t.Fatalf("outside approved scope did not wait: %+v calls=%d err=%v", waiting, calls, err)
			}
			approval, err := e.approvals.GetApproval(waiting.Tasks["task-1"].ApprovalID)
			if err != nil || approval.Status != ApprovalRequested || approval.ApprovedBy != "" {
				t.Fatalf("approval decided outside approved scope: %+v %v", approval, err)
			}
			if grantEvents(t, e, lane.RunID) != 0 {
				t.Fatal("grant event recorded outside approved scope")
			}
		})
	}
}

func TestExtremeApprovedPlanDeciderIsReserved(t *testing.T) {
	e, lane, base := approvedPlanLane(t, coveredGate())
	waiting, err := e.ExecuteTaskBound(t.Context(), lane.RunID, "task-1", base)
	if err != nil || waiting.State != RunNeedsApproval {
		t.Fatalf("unbound wait: %+v %v", waiting, err)
	}
	id := waiting.Tasks["task-1"].ApprovalID
	if err := e.approvals.Approve(id, ApprovedPlanDecider, ApprovedPlanReason, nowUTC()); !errors.Is(err, ErrApprovalDenied) {
		t.Fatalf("reserved decider accepted on the manager: %v", err)
	}
	if err := e.DecideApproval(t.Context(), id, true, ApprovedPlanDecider, ApprovedPlanReason); !errors.Is(err, ErrApprovalDenied) {
		t.Fatalf("reserved decider accepted on the engine: %v", err)
	}
	if err := e.DecideApproval(WithApprovedPlanExecution(t.Context(), approvedPlanBinding(lane)), id, true, ApprovedPlanDecider, ApprovedPlanReason); !errors.Is(err, ErrApprovalDenied) {
		t.Fatalf("approved-plan binding unlocked the reserved decider: %v", err)
	}
	approval, err := e.approvals.GetApproval(id)
	if err != nil || approval.Status != ApprovalRequested || IsApprovedPlanGrant(*approval) {
		t.Fatalf("reserved decider changed the approval: %+v %v", approval, err)
	}
}

// transportHarness pauses for brokered transport once, then succeeds only
// when the task carries an approved PROVIDER_TRANSPORT approval.
func transportHarness(e *Engine, calls *int) {
	e.RegisterHarness(NewMockHarness("mock", func(ctx context.Context, task TaskExecution, _ ConstraintPackage, _ string) (TaskResult, error) {
		*calls++
		if approval, err := e.approvals.GetApproval(task.ApprovalID); err == nil && approval.OperationType == "PROVIDER_TRANSPORT" && approval.Status == ApprovalApproved {
			return TaskResult{TaskID: task.TaskID, Success: true}, nil
		}
		req := ApprovalRequest{OperationType: "PROVIDER_TRANSPORT", TargetResource: "api.example:443", RiskLevel: "R3", Scope: "exact fixture binding", Parameters: "fixture", CurrentState: "fixture"}
		return e.ProviderTransportAdmissionPause(ctx, task, req), nil
	}))
}

func TestExtremeApprovedPlanTransportGrantedInSameRound(t *testing.T) {
	e, lane, base := approvedPlanLane(t, coveredGate())
	calls, checked := 0, 0
	transportHarness(e, &calls)
	b := approvedPlanBinding(lane)
	var originalID string
	b.Transport = func(_ context.Context, run ExecutionRun, task TaskExecution, original, transport RuntimeApproval) error {
		checked++
		originalID = original.ApprovalID
		if !IsApprovedPlanGrant(original) || transport.Status != ApprovalRequested || task.ApprovalID != transport.ApprovalID || run.RunID != lane.RunID {
			return errors.New("fixture binding differs")
		}
		return nil
	}
	done, err := e.ExecuteTaskBound(WithApprovedPlanExecution(t.Context(), b), lane.RunID, "task-1", base)
	if err != nil || done.Tasks["task-1"].State != TaskCompletedPendingVerify || calls != 2 || checked != 1 {
		t.Fatalf("approved-plan transport did not continue in the same round: %+v calls=%d checked=%d err=%v", done, calls, checked, err)
	}
	transport, err := e.approvals.GetApproval(done.Tasks["task-1"].ApprovalID)
	if err != nil || transport.OperationType != "PROVIDER_TRANSPORT" || !IsApprovedPlanGrant(*transport) {
		t.Fatalf("transport not granted by the runtime: %+v %v", transport, err)
	}
	if original, err := e.approvals.GetApproval(originalID); err != nil || !IsApprovedPlanGrant(*original) || original.OperationType != "TASK_EXECUTE" {
		t.Fatalf("chain original not the approved-plan grant: %+v %v", original, err)
	}
	if grantEvents(t, e, lane.RunID) != 2 {
		t.Fatal("both approved-plan grants must be journaled")
	}
}

func TestExtremeApprovedPlanTransportOutsideChainWaits(t *testing.T) {
	for _, name := range []string{"proof-refused", "no-proof-recorder", "operator-original"} {
		t.Run(name, func(t *testing.T) {
			e, lane, base := approvedPlanLane(t, coveredGate())
			calls := 0
			transportHarness(e, &calls)
			b := approvedPlanBinding(lane)
			b.Transport = func(context.Context, ExecutionRun, TaskExecution, RuntimeApproval, RuntimeApproval) error { return nil }
			switch name {
			case "proof-refused":
				b.Transport = func(context.Context, ExecutionRun, TaskExecution, RuntimeApproval, RuntimeApproval) error {
					return errors.New("endpoint is not the brokered provider")
				}
			case "no-proof-recorder":
				b.Transport = nil
			case "operator-original":
				waiting, err := e.ExecuteTaskBound(t.Context(), lane.RunID, "task-1", base)
				if err != nil || waiting.State != RunNeedsApproval {
					t.Fatalf("operator wait: %+v %v", waiting, err)
				}
				if err := e.DecideApproval(t.Context(), waiting.Tasks["task-1"].ApprovalID, true, "fixture-operator", "operator decision"); err != nil {
					t.Fatal(err)
				}
			}
			paused, err := e.ExecuteTaskBound(WithApprovedPlanExecution(t.Context(), b), lane.RunID, "task-1", base)
			if err != nil || paused.State != RunNeedsApproval || paused.Tasks["task-1"].State != TaskNeedsApproval || calls != 1 {
				t.Fatalf("transport outside the approved-plan chain did not wait: %+v calls=%d err=%v", paused, calls, err)
			}
			transport, err := e.approvals.GetApproval(paused.Tasks["task-1"].ApprovalID)
			if err != nil || transport.OperationType != "PROVIDER_TRANSPORT" || transport.Status != ApprovalRequested {
				t.Fatalf("transport decided outside the chain: %+v %v", transport, err)
			}
		})
	}
}
