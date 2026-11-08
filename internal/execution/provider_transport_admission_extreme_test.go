//go:build !governed

package execution

import (
	"context"
	"testing"
)

func TestProviderTransportAdmissionMarkerIsExactAndPrivate(t *testing.T) {
	e, err := NewEngine(EngineConfig{ProjectRoot: t.TempDir()}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	task := TaskExecution{RunID: "run-admission", RunRevision: 1, TaskID: "task", CanonicalTaskID: "TASK-real", AssignedHarness: "codex", State: TaskRunning}
	run := ExecutionRun{RunID: task.RunID, State: RunRunning, Tasks: map[string]TaskExecution{task.TaskID: task}}
	if err := e.RunStore().CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	req := ApprovalRequest{OperationType: "PROVIDER_TRANSPORT", TargetResource: "chatgpt.com:443", Scope: "exact broker API", CurrentState: "exact canonical binding"}
	outside := e.ProviderTransportAdmissionPause(context.Background(), task, req)
	if e.ownsProviderTransportAdmission(outside, run, task) {
		t.Fatal("outside active invocation minted no-call marker")
	}
	e.activeRuns[task.RunID] = struct{}{}
	exact := e.ProviderTransportAdmissionPause(context.Background(), task, req)
	if !e.ownsProviderTransportAdmission(exact, run, task) {
		t.Fatal("exact active admission lacks private marker")
	}
	for _, name := range []string{"public", "foreignEngine", "changedRequest", "nativeTurn", "nativeReceipt", "tokens", "revision", "taskID", "canonicalNative", "duration", "success", "evidence", "claims", "files", "error", "parent"} {
		t.Run(name, func(t *testing.T) {
			result := exact
			owner := e
			current := task
			currentRun := run
			switch name {
			case "public":
				result = TaskResult{TaskID: task.TaskID, NeedsApproval: true, ApprovalReq: &req}
			case "foreignEngine":
				owner = &Engine{}
			case "changedRequest":
				changed := req
				changed.TargetResource = "192.168.1.1:443"
				result.ApprovalReq = &changed
			case "nativeTurn":
				result.NativeTurn = &NativeTurnBinding{Provider: "codex"}
			case "nativeReceipt":
				result.NativeApprovalID = "native-actual-model-called"
			case "tokens":
				result.InputTokens = 1
			case "taskID":
				result.TaskID = "foreign"
			case "canonicalNative":
				current.NativeTurn = &NativeTurnBinding{Provider: "codex"}
			case "duration":
				result.Duration = 1
			case "success":
				result.Success = true
			case "evidence":
				result.EvidenceList = []ExecutionEvidence{{}}
			case "claims":
				result.Claims = []ExecutionClaim{{}}
			case "files":
				result.FilesModified = []string{"README.md"}
			case "parent":
				currentRun.ParentRunID = "foreign-parent"
			case "error":
				result.ErrorMessage = "actual provider observation"
			case "revision":
				current.RunRevision++
			}
			if owner.ownsProviderTransportAdmission(result, currentRun, current) {
				t.Fatal("forged or actual-provider result skipped model accounting")
			}
		})
	}
}

func TestProviderTransportAdmissionCannotSealStaleNativeSnapshot(t *testing.T) {
	e, err := NewEngine(EngineConfig{ProjectRoot: t.TempDir()}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	task := TaskExecution{RunID: "run", TaskID: "task", CanonicalTaskID: "TASK", RunRevision: 1, AssignedHarness: "codex", State: TaskRunning}
	actual := task
	actual.NativeTurn = &NativeTurnBinding{Provider: "codex"}
	run := ExecutionRun{RunID: task.RunID, State: RunRunning, Tasks: map[string]TaskExecution{task.TaskID: actual}}
	if err := e.RunStore().CreateRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	e.activeRuns[task.RunID] = struct{}{}
	req := ApprovalRequest{OperationType: "PROVIDER_TRANSPORT", TargetResource: "chatgpt.com:443", Scope: "exact", CurrentState: "exact"}
	result := e.ProviderTransportAdmissionPause(t.Context(), task, req)
	if result.transportAdmission != nil {
		t.Fatal("stale task snapshot sealed current native invocation")
	}
}

func TestProviderTransportReservationRejectsUnsealedAndReplay(t *testing.T) {
	e, err := NewEngine(EngineConfig{ProjectRoot: t.TempDir()}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	task := TaskExecution{RunID: "run", TaskID: "task", CanonicalTaskID: "TASK", RunRevision: 1, AssignedHarness: "codex", State: TaskRunning}
	run := ExecutionRun{RunID: task.RunID, State: RunRunning, Tasks: map[string]TaskExecution{task.TaskID: task}}
	if err := e.RunStore().CreateRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	e.activeRuns[task.RunID] = struct{}{}
	req := ApprovalRequest{OperationType: "PROVIDER_TRANSPORT", TargetResource: "chatgpt.com:443", Scope: "exact", CurrentState: "exact"}
	forged := TaskResult{TaskID: task.TaskID, NeedsApproval: true, ApprovalReq: &req}
	if err := e.releaseProviderTransportReservation(t.Context(), forged, run, task); err == nil {
		t.Fatal("public result refunded reservation")
	}
	sealed := e.ProviderTransportAdmissionPause(t.Context(), task, req)
	if err := e.releaseProviderTransportReservation(t.Context(), sealed, run, task); err != nil {
		t.Fatal(err)
	}
	if err := e.releaseProviderTransportReservation(t.Context(), sealed, run, task); err == nil {
		t.Fatal("sealed admission replay refunded twice")
	}
}
