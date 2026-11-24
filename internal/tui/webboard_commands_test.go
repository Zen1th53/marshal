package tui

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/webboard"
)

func workTestAction(t *testing.T, w *Workspace, cap webboard.SettingCapability, payload webboard.SettingPayload) webboard.Action {
	t.Helper()
	state := w.webSettingsState()
	raw, _ := json.Marshal(payload)
	i := store.WebIntent{ProjectID: state.ProjectID, ProjectEpoch: webboard.SettingsEpoch(state.Fence), Owner: "owner", Session: "browser", RequestID: "00112233445566778899aabbccddeeff", Digest: cap.Op + cap.AttemptID + string(raw), Op: cap.Op, Key: cap.Target.ID, TargetKind: cap.Target.Kind, Value: string(raw), Revision: cap.Revision, RunID: cap.RunID, AttemptID: cap.AttemptID}
	if _, err := w.store.AdmitWebSetting(t.Context(), i); err != nil {
		t.Fatal(err)
	}
	return webboard.Action{Kind: "cmd", Command: &i, Commit: func(effect func() webboard.Result) webboard.Result { return effect() }}
}

func workTestCapability(t *testing.T, w *Workspace, op string) webboard.SettingCapability {
	t.Helper()
	for _, c := range w.webWorkCapabilities(t.Context()) {
		if c.Op == op {
			return c
		}
	}
	t.Fatalf("missing capability %s", op)
	return webboard.SettingCapability{}
}

func TestTaskCommandStaleAttemptRefused(t *testing.T) {
	w, _ := receiptTestWorkspace(t, "browser-stale")
	service, rev := seedMarshalRunForReceiptTest(t, w, "work", marshal.Run{State: marshal.Dispatching, Tasks: []marshal.Task{{PlanTaskID: "one", Worker: "codex", State: marshal.Returned}}})
	a := workTestAction(t, w, workTestCapability(t, w, "task/retry"), webboard.SettingPayload{})
	run, _ := service.Snapshot(t.Context(), "work")
	run.Tasks[0].FreshSessionRetries++
	if _, err := service.Store.SetMarshalRun(t.Context(), service.ProjectID, "work", run, rev); err != nil {
		t.Fatal(err)
	}
	if result := w.webWorkCommand(a); result.Status != "refused" {
		t.Fatalf("stale: %+v", result)
	}
	after, _ := service.Snapshot(t.Context(), "work")
	if len(after.Commands) != 0 || after.Tasks[0].FreshSessionRetries != 1 {
		t.Fatalf("stale effect: %+v", after)
	}
}

func TestSkipRequiresReplan(t *testing.T) {
	w, _ := receiptTestWorkspace(t, "browser-skip")
	service, _ := seedMarshalRunForReceiptTest(t, w, "work", marshal.Run{State: marshal.Dispatching, Tasks: []marshal.Task{{PlanTaskID: "one", Worker: "codex", State: marshal.Escalated, EscalationReason: "needs help"}, {PlanTaskID: "sibling", Worker: "claude", State: marshal.Dispatched}}})
	a := workTestAction(t, w, workTestCapability(t, w, "task/skip"), webboard.SettingPayload{})
	if result := w.webWorkCommand(a); result.Status != "done" {
		t.Fatalf("skip: %+v", result)
	}
	run, _ := service.Snapshot(t.Context(), "work")
	if run.Tasks[0].State != marshal.Cancelled || run.Tasks[1].State != marshal.Dispatched || run.Pause == nil || run.Pause.Action != marshal.PauseUsePlan || run.ValidCloseAuthorization() || len(run.Commands) != 1 || run.Commands[0].State != "applied" {
		t.Fatalf("cancel scope: %+v", run)
	}
	receipt, err := w.store.WebReceipt(t.Context(), a.Command.ProjectID, a.Command.Owner, a.Command.RequestID, a.Command.Digest)
	if err != nil || receipt.Status != "applied" {
		t.Fatalf("receipt: %+v %v", receipt, err)
	}
}

func TestWebOpMatrixUsesExistingAuthorityAndPayload(t *testing.T) {
	for op, want := range map[string]string{"task/retry": "retry", "task/retry.debug": "retry", "task/reassign": "reassign", "task/model.set": "reassign", "task/skip": "cancel", "task/accept": "accept", "task/return": "return"} {
		t.Run(op, func(t *testing.T) {
			i := store.WebIntent{Op: op, Key: "selected", AttemptID: "attempt"}
			input, err := webRunInput(i, webboard.SettingPayload{Provider: "claude", Reason: "review reason"})
			if err != nil || input.Operation != want || input.TaskID != i.Key || input.TaskDigest != i.AttemptID || input.Worker != "claude" || input.Reason != "review reason" || ((want == "retry") != input.FreshSession) {
				t.Fatalf("input: %+v %v", input, err)
			}
		})
	}
	t.Run("approval writer", testWebTaskApprovalWriter)
	t.Run("retry receipt and replay", func(t *testing.T) {
		w, _ := receiptTestWorkspace(t, "browser-retry")
		service, _ := seedMarshalRunForReceiptTest(t, w, "work", marshal.Run{State: marshal.Dispatching, Tasks: []marshal.Task{{PlanTaskID: "one", Worker: "codex", State: marshal.Returned}, {PlanTaskID: "sibling", Worker: "claude", State: marshal.Dispatched}}})
		a := workTestAction(t, w, workTestCapability(t, w, "task/retry.debug"), webboard.SettingPayload{})
		w.marshalSession().mu.Lock()
		w.marshalSession().busy = true
		w.marshalSession().mu.Unlock()
		if result := w.webWorkCommand(a); result.Status != "done" {
			t.Fatalf("retry while busy: %+v", result)
		}
		_ = w.webWorkCommand(a)
		run, _ := service.Snapshot(t.Context(), "work")
		if len(run.Commands) != 1 || run.Commands[0].State != "applied" || run.Tasks[0].FreshSessionRetries != 1 || run.Tasks[1].State != marshal.Dispatched {
			t.Fatalf("retry replay: %+v", run)
		}
	})
}

func testWebTaskApprovalWriter(t *testing.T) {
	w, rt := receiptTestWorkspace(t, "browser-approval")
	service, _ := seedMarshalRunForReceiptTest(t, w, "work", marshal.Run{State: marshal.Dispatching, BaseCommit: "base", Tasks: []marshal.Task{{PlanTaskID: "one", Worker: "codex", Mode: marshal.Governed, State: marshal.Dispatched}}})
	run, _ := service.Snapshot(t.Context(), "work")
	manager := rt.Execution().Engine().ApprovalManager()
	approval, err := manager.RequestApproval(execution.ApprovalRequest{RunID: "execution", TaskID: "one", PlanID: run.PlanID, PlanVersion: run.PlanVersion, OperationType: "TASK_EXECUTE", TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	authority := w.controlSource().Authority.(*runtimeControlAuthority)
	executionRun := execution.ExecutionRun{RunID: "execution", SessionID: authority.sessionID, ProjectID: service.CanonicalPlanProjectID(), PlanID: run.PlanID, PlanVersion: run.PlanVersion, BaseCommit: run.BaseCommit, Delivery: execution.DeliveryPreserveBranch, State: execution.RunNeedsApproval, Tasks: map[string]execution.TaskExecution{"one": {TaskID: "one", AssignedHarness: "codex", State: execution.TaskNeedsApproval, ApprovalID: approval.ApprovalID}}}
	if err = rt.Execution().Engine().RunStore().CreateRun(t.Context(), executionRun); err != nil {
		t.Fatal(err)
	}
	if err = rt.RecordApprovalOpened(t.Context(), "work", *approval); err != nil {
		t.Fatal(err)
	}
	cap := workTestCapability(t, w, "task/approve-task")
	if cap.ApprovalID != approval.ApprovalID {
		t.Fatal("unbound approval")
	}
	a := workTestAction(t, w, cap, webboard.SettingPayload{ApprovalID: approval.ApprovalID})
	if result := w.webWorkCommand(a); result.Status != "done" {
		t.Fatalf("approval: %+v", result)
	}
	after, err := manager.GetApproval(approval.ApprovalID)
	if err != nil || after.Status != execution.ApprovalApproved || after.DecisionCommandKey == "" {
		t.Fatalf("approval writer: %+v %v", after, err)
	}
	receipt, err := w.store.WebReceipt(t.Context(), a.Command.ProjectID, a.Command.Owner, a.Command.RequestID, a.Command.Digest)
	if err != nil || receipt.Status != "applied" {
		t.Fatalf("approval receipt: %+v %v", receipt, err)
	}
	if result := w.webWorkCommand(a); result.Status != "refused" {
		t.Fatalf("stale approval: %+v", result)
	}
}

func TestWebLaneMutationRechecksAttemptAtDequeue(t *testing.T) {
	w, _ := receiptTestWorkspace(t, "browser-lane")
	task := marshal.Task{PlanTaskID: "one", Worker: "codex", State: marshal.Dispatched}
	agent := &activeTmuxAgent{id: "lane", role: "task", runID: "work", taskID: "one", paneID: "pane", pid: 1, state: "working"}
	agent.supervisor.PID = 2
	agent.supervisor.Start = "original"
	w.tmuxActiveWins = map[string]*activeTmuxAgent{"lane": agent}
	i := store.WebIntent{Key: "lane", RunID: "work", AttemptID: webLaneBinding(task, agent)}
	if _, _, err := w.webBoundLane(i, marshal.Run{Tasks: []marshal.Task{task}}); err != nil {
		t.Fatal(err)
	}
	task.FreshSessionRetries++
	if _, _, err := w.webBoundLane(i, marshal.Run{Tasks: []marshal.Task{task}}); err == nil {
		t.Fatal("new attempt accepted")
	}
	task.FreshSessionRetries--
	agent.supervisor.Start = "replacement"
	if _, _, err := w.webBoundLane(i, marshal.Run{Tasks: []marshal.Task{task}}); err == nil {
		t.Fatal("replacement process accepted")
	}
	agent.supervisor.Start = "original"
	agent.role = "marshal-chat"
	if _, _, err := w.webBoundLane(i, marshal.Run{Tasks: []marshal.Task{task}}); err == nil {
		t.Fatal("chat accepted as worker")
	}
}
