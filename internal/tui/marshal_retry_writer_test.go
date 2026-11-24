package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
)

func TestActiveRunQueuesRetryAndNamesExecution(t *testing.T) {
	w, _ := receiptTestWorkspace(t, "retry-writer")
	const runID = "retry-run"
	service, _ := seedMarshalRunForReceiptTest(t, w, runID, marshal.Run{
		State: marshal.Dispatching,
		Tasks: []marshal.Task{
			{PlanTaskID: "retry", Worker: "codex", State: marshal.Returned},
			{PlanTaskID: "sibling", Worker: "codex", State: marshal.Dispatched},
		},
	})
	// Hold the returned task at admission while the execution writer waits.
	service.HeldTask = func(string, string) bool { return true }
	changed := make(chan struct{})
	waiting := make(chan struct{}, 1)
	service.HoldChanged = func() <-chan struct{} {
		select {
		case waiting <- struct{}{}:
		default:
		}
		return changed
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = service.Execute(ctx, runID, func(marshal.Task, app.BriefContext) string { return "work" }, nil)
	}()
	defer func() { cancel(); <-done }()
	select {
	case <-waiting:
	case <-ctx.Done():
		t.Fatal("writer did not wait")
	}
	execution := service.ActiveExecution(runID)
	if execution == "" {
		t.Fatal("no execution identity")
	}
	w.marshalSession().mu.Lock()
	w.marshalSession().busy = true
	w.marshalSession().mu.Unlock()
	answer, err := boardRetry(w, ctx, "retry")
	if err != nil || !strings.Contains(answer, "queued") || !strings.Contains(answer, execution) {
		t.Fatalf("producer receipt: %q %v", answer, err)
	}
	for {
		run, err := service.Snapshot(ctx, runID)
		if err != nil {
			t.Fatal(err)
		}
		if len(run.Commands) == 1 && run.Commands[0].State == "applied" {
			if run.Tasks[0].FreshSessionRetries != 1 || run.Tasks[1].State != marshal.Dispatched {
				t.Fatalf("task effects: %+v", run.Tasks)
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("no terminal receipt")
		case <-time.After(time.Millisecond):
		}
	}
}

func TestLegacyTaskCommandUsesBoundWriter(t *testing.T) {
	w, _ := receiptTestWorkspace(t, "task-writer")
	const runID = "idle-retry"
	service, _ := seedMarshalRunForReceiptTest(t, w, runID, marshal.Run{State: marshal.Dispatching, Tasks: []marshal.Task{{PlanTaskID: "retry", Worker: "codex", State: marshal.Returned}}})
	answer, err := w.cmd.handleMarshal(t.Context(), []string{"retry", "retry", "fresh"})
	if err != nil || !strings.Contains(answer, "applied") {
		t.Fatalf("terminal receipt: %q %v", answer, err)
	}
	run, err := service.Snapshot(t.Context(), runID)
	if err != nil || len(run.Commands) != 1 || run.Commands[0].State != "applied" || !run.Commands[0].FreshSession {
		t.Fatalf("journal: %+v %v", run.Commands, err)
	}
}

func TestTaskProposalReplaysWriterReceipt(t *testing.T) {
	w, _ := receiptTestWorkspace(t, "proposal-writer")
	const runID = "proposal-retry"
	service, _ := seedMarshalRunForReceiptTest(t, w, runID, marshal.Run{State: marshal.Dispatching, Tasks: []marshal.Task{{PlanTaskID: "retry", Worker: "codex", State: marshal.Returned}}})
	req := queueAutomaticProposalRequest(t, w, marshalProposal{action: "retry", taskID: "retry", freshSession: true, occurrence: "runtime:retry-once"})
	for range 2 {
		if err := w.applyMarshalProposal(withOwnerActionContext(t.Context()), req); err != nil {
			t.Fatal(err)
		}
	}
	run, err := service.Snapshot(t.Context(), runID)
	if err != nil || len(run.Commands) != 1 || run.Tasks[0].FreshSessionRetries != 1 {
		t.Fatalf("replayed retry: %+v %v", run, err)
	}
	// A bound request cannot silently select a newer attempt.
	a := w.controlSource().Authority.(*runtimeControlAuthority)
	_, err = service.CommandRun(a.localControl.Context(t.Context()), app.CommandEnvelope{ProjectID: a.runtime.ProjectIdentity(), SessionID: runID, TargetID: "retry", IdempotencyKey: "stale-request"}, app.RunCommandInput{Operation: "retry", TaskID: "retry", FreshSession: true, TaskDigest: req.ApprovalBinding})
	if err == nil {
		t.Fatal("stale task binding admitted")
	}
	after, _ := service.Snapshot(t.Context(), runID)
	if len(after.Commands) != 1 || after.Tasks[0].FreshSessionRetries != 1 {
		t.Fatal("stale request changed task or journal")
	}
}
