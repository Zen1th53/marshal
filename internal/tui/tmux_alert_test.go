package tui

import (
	"context"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/tmux"
	"io"
)

func TestTaskAlertsDriveStatusChatAndFollowActive(t *testing.T) {
	w := realTmuxWorkspace(t)
	ctx := context.Background()
	if err := tmux.NewWindow(ctx, w.tmuxSession, "worker", w.workDir, nil, []string{"sleep", "30"}); err != nil {
		t.Fatal(err)
	}
	panes, _ := tmux.ListPanes(ctx, w.tmuxSession)
	var pane string
	for _, p := range panes {
		if p.WindowName == "worker" {
			pane = p.PaneID
		}
	}
	w.tmuxMu.Lock()
	w.tmuxActiveWins["task-one"] = &activeTmuxAgent{id: "task-one", role: "task", taskID: "one", runID: "run", canonicalTaskID: "canonical-one", executionRunID: "process05", paneID: pane, window: "worker", label: "Task one", state: "working"}
	w.tmuxMu.Unlock()
	// Waiting alerts do not steal focus unless the operator enables follow mode.
	if err := w.deliverEgressAlert(app.EgressAlert{RunID: "egress", ParentRunID: "process05", TaskID: "canonical-one", Worker: "worker", State: "waiting", Kind: "egress refused", Message: "initial waiting alert"}); err != nil {
		t.Fatal(err)
	}
	selectedBeforeFollow, err := tmux.RunCommand(ctx, "display-message", "-p", "-t", w.tmuxSession, "#{window_index}")
	if err != nil || strings.TrimSpace(string(selectedBeforeFollow)) != "0" {
		t.Fatalf("alert stole control centre focus: %q (%v)", selectedBeforeFollow, err)
	}
	if _, err := w.handleViewCommand(ctx, []string{"follow"}); err != nil {
		t.Fatal(err)
	}
	if err := w.deliverEgressAlert(app.EgressAlert{RunID: "egress", ParentRunID: "process05", TaskID: "canonical-one", Worker: "worker", State: "waiting", Kind: "egress refused", Message: "worker needs endpoint", Endpoint: "localhost:80"}); err != nil {
		t.Fatal(err)
	}
	selected, _ := tmux.RunCommand(ctx, "display-message", "-p", "-t", w.tmuxSession, "#{window_name}")
	if strings.TrimSpace(string(selected)) != "worker" {
		t.Fatalf("follow-active did not follow task: %q", selected)
	}
	status, _ := tmux.RunCommand(ctx, "show-options", "-w", "-v", "-t", w.tmuxMarshalWin, "@marshal_status")
	if !strings.Contains(string(status), "waiting") {
		t.Fatalf("no task waiting status: %q", status)
	}
	if err := w.deliverEgressAlert(app.EgressAlert{RunID: "process05", TaskID: "canonical-one", Worker: "worker", State: "failed", Kind: "honeypot", Message: "worker stopped; do not merge"}); err != nil {
		t.Fatal(err)
	}
	status, _ = tmux.RunCommand(ctx, "show-options", "-w", "-v", "-t", w.tmuxMarshalWin, "@marshal_status")
	if !strings.Contains(string(status), "honeypot") {
		t.Fatalf("incident missing from status: %q", status)
	}
	inbox, err := os.ReadFile(filepath.Join(w.workDir, ".marshal", "inbox", "marshal.md"))
	if err != nil || !strings.Contains(string(inbox), "worker needs endpoint") || !strings.Contains(string(inbox), "worker stopped; do not merge") {
		t.Fatalf("alerts missing from Marshal inbox: %q %v", inbox, err)
	}
}

func TestNativeWaitingIsBoundToTheActualTask(t *testing.T) {
	runs := []execution.ExecutionRun{{ProjectID: projectid.ID("project"), PlanID: "plan", PlanVersion: 2, State: execution.RunNeedsApproval, Tasks: map[string]execution.TaskExecution{"waiting": {TaskID: "waiting", State: execution.TaskNeedsApproval, NativeTurn: &execution.NativeTurnBinding{}}, "working": {TaskID: "working", State: execution.TaskRunning}}}}
	if !taskNativeWaiting(runs, "project", "plan", 2, "waiting") {
		t.Fatal("real waiting task was ignored")
	}
	for _, task := range []string{"working", "absent"} {
		if taskNativeWaiting(runs, "project", "plan", 2, task) {
			t.Fatalf("unrelated task %s marked waiting", task)
		}
	}
	if taskNativeWaiting(runs, "other-project", "plan", 2, "waiting") || taskNativeWaiting(runs, "project", "plan", 3, "waiting") {
		t.Fatal("another project/plan version influenced task waiting")
	}
}

func TestTerminalCompletionAlertUsesDriverExitStatus(t *testing.T) {
	w := realTmuxWorkspace(t)
	d := &tmuxTaskDriver{w: w, inner: driver.Native{Provider: "test", Binary: "/bin/sh", Args: func(driver.Request) []string { return []string{"-c", "echo real-worker; exit 7"} }, Parse: func([]byte) []marshal.CommandRecord { return nil }}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	h, err := d.Launch(ctx, realTaskRequest(t, w, "exit-status"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Cancel(h)
	<-h.Done()
	selected, err := tmux.RunCommand(ctx, "display-message", "-p", "-t", w.tmuxSession, "#{window_index}")
	if err != nil || strings.TrimSpace(string(selected)) != "0" {
		t.Fatalf("worker completion stole control centre focus: %q (%v)", selected, err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		status, _ := tmux.RunCommand(ctx, "show-options", "-w", "-v", "-t", w.tmuxMarshalWin, "@marshal_status")
		if strings.Contains(string(status), ": failed") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("completed worker did not publish its real failure: %q", status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAlertsAndClearingStayWithinRun(t *testing.T) {
	for _, kind := range []string{"worker done", "task waiting", "honeypot"} {
		t.Run(kind, func(t *testing.T) {
			w := NewWorkspace(nil, "project", "session")
			w.workDir = t.TempDir()
			first := &activeTmuxAgent{id: "first", role: "task", taskID: "same", runID: "first", canonicalTaskID: "canonical", executionRunID: "exec-first", state: "working"}
			second := &activeTmuxAgent{id: "second", role: "task", taskID: "same", runID: "second", canonicalTaskID: "canonical", executionRunID: "exec-second", state: "working"}
			w.tmuxActiveWins = map[string]*activeTmuxAgent{"first": first, "second": second}
			alert := app.EgressAlert{RunID: "first", TaskID: "same", Kind: kind, State: "waiting", Message: "first run only"}
			if kind == "honeypot" {
				alert.RunID, alert.ParentRunID, alert.TaskID = "child", "exec-first", "canonical"
			}
			if err := w.deliverEgressAlert(alert); err != nil {
				t.Fatal(err)
			}
			if first.state != "waiting" || second.state != "working" {
				t.Fatalf("cross-run delivery: %s / %s", first.state, second.state)
			}
			w.tmuxAlerts["second:same"], w.tmuxAlerts["exec-second:canonical"] = "task waiting: waiting", "egress refused: waiting"
			d := driver.Governed{Run: func(context.Context, driver.Request) ([]marshal.CommandRecord, error) { return nil, nil }}
			h, err := d.Launch(context.Background(), driver.Request{RunID: "first", Task: marshal.Task{PlanTaskID: "same", BaseCommit: "base"}, Worktree: w.workDir, Brief: "approved"})
			if err != nil {
				t.Fatal(err)
			}
			defer d.Cancel(h)
			w.monitorTaskState(context.Background(), first, h)
			if first.state != "done" || second.state != "working" || w.tmuxAlerts["second:same"] != "task waiting: waiting" || w.tmuxAlerts["exec-second:canonical"] != "egress refused: waiting" {
				t.Fatalf("cross-run completion/clearing: %s / %s %v", first.state, second.state, w.tmuxAlerts)
			}
			if w.tmuxAlerts["first:same"] != "worker done: done" {
				t.Fatal("completion omitted owning run")
			}
		})
	}
}

func TestStartupReplaysIncidentWithRunIdentity(t *testing.T) {
	t.Setenv("MARSHAL_NO_UPDATE_CHECK", "1")
	st, w, ctx := newMutationWorkspace(t)
	w.workDir = t.TempDir()
	w.tmuxActiveWins = map[string]*activeTmuxAgent{
		"first":  {role: "task", taskID: "same", runID: "first", state: "working"},
		"second": {role: "task", taskID: "same", runID: "second", state: "working"},
	}
	if err := st.AppendEvent(ctx, nil, model.Event{ID: "EVENT-incident", Type: "HONEYPOT_HIT", ProjectID: "PROJECT-mut", Timestamp: time.Now().UTC(), Data: map[string]any{"task_id": "same", "run_id": "first", "parent_run_id": "first"}}); err != nil {
		t.Fatal(err)
	}
	if err := w.Run(ctx, strings.NewReader(""), io.Discard); err != nil {
		t.Fatal(err)
	}
	if w.tmuxActiveWins["first"].state != "failed" || w.tmuxActiveWins["second"].state != "working" {
		t.Fatal("startup incident replay lost run identity")
	}
	data, err := os.ReadFile(filepath.Join(w.workDir, ".marshal", "inbox", "marshal.md"))
	if err != nil || !strings.Contains(string(data), "honeypot hit") {
		t.Fatalf("startup incident missing from inbox: %s %v", data, err)
	}
}
