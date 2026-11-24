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

func TestWebStopWorkersStopsOnlyBoundRunWorkers(t *testing.T) {
	w, _ := receiptTestWorkspace(t, "browser-stop-workers")
	seedMarshalRunForReceiptTest(t, w, "work", marshal.Run{State: marshal.Dispatching, Tasks: []marshal.Task{{PlanTaskID: "one", Worker: "codex", State: marshal.Dispatched}, {PlanTaskID: "two", Worker: "claude", State: marshal.Dispatched}}})
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
	one, oneRef, oneDone := start()
	two, twoRef, twoDone := start()
	_, otherRef, _ := start()
	time.Sleep(50 * time.Millisecond)
	path := filepath.Join(t.TempDir(), "tmux")
	script := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in\nlist-panes) printf '%%s\\t%%s\\t%%s\\t%%s\\t0\\t0\\tworker\\n' '%%1' '@1' 'one' '%d' '%%2' '@1' 'two' '%d';;\n*) exit 1;;\nesac\n", one.Process.Pid, two.Process.Pid)
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(path)
	t.Cleanup(tmux.ResetBinaryPath)
	w.tmuxActiveWins = map[string]*activeTmuxAgent{
		"one":   {id: "one", role: "task", runID: "work", taskID: "one", paneID: "%1", pid: one.Process.Pid, supervisor: oneRef, state: "working", doneChan: oneDone},
		"two":   {id: "two", role: "task", runID: "work", taskID: "two", paneID: "%2", pid: two.Process.Pid, supervisor: twoRef, state: "working", doneChan: twoDone},
		"other": {id: "other", role: "task", runID: "elsewhere", taskID: "x", paneID: "%3", pid: 1, supervisor: otherRef, state: "working"},
	}
	a := runTestAction(t, w, mustRunCapability(t, w, "run/stop-workers"), "90112233445566778899aabbccddeeff", webboard.SettingPayload{})
	if r := w.webWorkCommand(a); r.Status != "done" {
		t.Fatalf("stop-workers: %+v", r)
	}
	for name, ref := range map[string]processgroup.Reference{"one": oneRef, "two": twoRef} {
		if alive, err := processgroup.RunningReference(ref); err != nil || alive {
			t.Fatalf("%s did not exit: %v %v", name, alive, err)
		}
	}
	if alive, err := processgroup.RunningReference(otherRef); err != nil || !alive {
		t.Fatalf("another run's worker was touched: %v %v", alive, err)
	}
	r := runReceipt(t, w, a)
	if r.Status != "applied" || len(r.Effects) != 2 || r.Effects[0] != "one:exited" || r.Effects[1] != "two:exited" {
		t.Fatalf("receipt: %+v", r)
	}
}
