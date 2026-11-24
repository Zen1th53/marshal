//go:build linux

package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/processgroup"
	"github.com/Zen1th53/marshal/internal/tmux"
	"github.com/Zen1th53/marshal/internal/webboard"
)

func TestLaneRestartTouchesOnlyBoundPane(t *testing.T) {
	w, _ := receiptTestWorkspace(t, "browser-pane")
	service, _ := seedMarshalRunForReceiptTest(t, w, "work", marshal.Run{State: marshal.Dispatching, Tasks: []marshal.Task{{PlanTaskID: "one", Worker: "codex", State: marshal.Escalated, EscalationReason: "worker interrupted"}, {PlanTaskID: "sibling", Worker: "claude", State: marshal.Dispatched}}})
	start := func() (*exec.Cmd, processgroup.Reference, chan struct{}) {
		t.Helper()
		cmd := exec.Command("sleep", "60")
		if err := processgroup.Wrap(cmd); err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		ref, err := processgroup.Identify(cmd)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		t.Cleanup(func() { _ = processgroup.StopReference(ref); <-done })
		return cmd, ref, done
	}
	selected, selectedRef, done := start()
	_, siblingRef, _ := start()
	_, chatRef, _ := start()
	time.Sleep(50 * time.Millisecond)
	path := filepath.Join(t.TempDir(), "tmux")
	// Only inventory reads are allowed; restart must address the pinned supervisor.
	script := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in\nlist-panes) printf '%%s\\t%%s\\t%%s\\t%%s\\t0\\t0\\tworker\\n' '%%1' '@1' 'worker' '%d';;\n*) exit 1;;\nesac\n", selected.Process.Pid)
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(path)
	t.Cleanup(tmux.ResetBinaryPath)
	w.tmuxActiveWins = map[string]*activeTmuxAgent{"selected": {id: "selected", role: "task", runID: "work", taskID: "one", paneID: "%1", pid: selected.Process.Pid, supervisor: selectedRef, state: "working", readOnly: true, doneChan: done}}
	cap := workTestCapability(t, w, "lane/restart")
	a := workTestAction(t, w, cap, webboard.SettingPayload{})
	if result := w.webWorkCommand(a); result.Status != "done" {
		t.Fatalf("restart: %+v", result)
	}
	for name, ref := range map[string]processgroup.Reference{"sibling": siblingRef, "chat": chatRef} {
		if alive, err := processgroup.RunningReference(ref); err != nil || !alive {
			t.Fatalf("%s was touched: %v %v", name, alive, err)
		}
	}
	if alive, err := processgroup.RunningReference(selectedRef); err != nil || alive {
		t.Fatalf("selected worker did not exit: %v %v", alive, err)
	}
	run, _ := service.Snapshot(t.Context(), "work")
	if run.Tasks[0].FreshSessionRetries != 1 || run.Tasks[1].State != marshal.Dispatched || len(run.Commands) != 1 || run.Commands[0].State != "applied" {
		t.Fatalf("restart retry: %+v", run)
	}
	// A replay must neither stop a replacement nor perform a second retry.
	_ = w.webWorkCommand(a)
	run, _ = service.Snapshot(t.Context(), "work")
	if run.Tasks[0].FreshSessionRetries != 1 {
		t.Fatal("restart replayed")
	}
}
