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
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/tmux"
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
	w.tmuxFollowActive = true
	w.tmuxActiveWins["task-one"] = &activeTmuxAgent{id: "task-one", role: "task", taskID: "one", runID: "run", canonicalTaskID: "canonical-one", executionRunID: "process05", paneID: pane, window: "worker", label: "Task one", state: "working"}
	w.tmuxMu.Unlock()
	if err := w.deliverEgressAlert(app.EgressAlert{RunID: "egress", ParentRunID: "process05", TaskID: "canonical-one", Worker: "worker", State: "waiting", Kind: "egress refused", Message: "worker needs endpoint", Endpoint: "localhost:80"}); err != nil {
		t.Fatal(err)
	}
	selected, _ := tmux.RunCommand(ctx, "display-message", "-p", "-t", w.tmuxSession, "#{window_name}")
	if strings.TrimSpace(string(selected)) != "worker" {
		t.Fatalf("follow-active did not follow task: %q", selected)
	}
	status, _ := tmux.RunCommand(ctx, "show-options", "-v", "-t", w.tmuxSession, "status-right")
	if !strings.Contains(string(status), "waiting") {
		t.Fatalf("no task waiting status: %q", status)
	}
	if err := w.deliverEgressAlert(app.EgressAlert{RunID: "process05", TaskID: "canonical-one", Worker: "worker", State: "failed", Kind: "honeypot", Message: "worker stopped; do not merge"}); err != nil {
		t.Fatal(err)
	}
	status, _ = tmux.RunCommand(ctx, "show-options", "-v", "-t", w.tmuxSession, "status-right")
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
