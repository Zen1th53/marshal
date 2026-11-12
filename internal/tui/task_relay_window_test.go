//go:build linux

package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/tmux"
	"github.com/Zen1th53/marshal/internal/worker"
)

// A governed task runs its processes one after another (a version probe, the
// worker, ...), each on a terminal of its own. They all show in the task's one
// window: before, every process opened another window of the same name, and a
// task with three processes left three identical "Status: completed" windows.
func TestGovernedTaskProcessesShareOneWindow(t *testing.T) {
	w := realTmuxWorkspace(t)
	runner := worker.New(5*time.Second, 100*time.Millisecond, 1<<16)
	name := tmux.TaskWindowName("steps", w.workDir)
	var windows []string
	d := &tmuxTaskDriver{w: w, inner: driver.Governed{Run: func(ctx context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		for i := range 3 {
			if _, err := runner.Run(ctx, adapter.Command{Path: "/bin/sh", Args: []string{"-c", fmt.Sprintf("echo STEP_%d", i)}}); err != nil {
				return nil, err
			}
		}
		// Every process has run; the task's window is still open.
		var err error
		windows, err = tmux.ListWindows(ctx, w.tmuxSession)
		return nil, err
	}}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	h, err := d.Launch(ctx, realTaskRequest(t, w, "steps"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Wait(ctx, h); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, win := range windows {
		if win == name {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("the task's processes opened %d windows named %s, want 1: %s", n, name, strings.Join(windows, ", "))
	}
	// The task's end closes its one window: nothing is left behind.
	after, err := tmux.ListWindows(ctx, w.tmuxSession)
	if err != nil {
		t.Fatal(err)
	}
	for _, win := range after {
		if win == name {
			t.Fatalf("a window of the finished task is left open: %s", strings.Join(after, ", "))
		}
	}
}
