package tui

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
)

func TestGovernedTmuxInvokesCanonicalRunner(t *testing.T) {
	setupFakeTmuxWithDeadFile(t)
	w := NewWorkspace(nil, "project", "session")
	w.workDir = t.TempDir()
	w.tmuxSession = "test-session"
	w.tmuxActiveWins = make(map[string]*activeTmuxAgent)
	called := make(chan struct{})
	d := &tmuxTaskDriver{w: w, inner: driver.Governed{Run: func(ctx context.Context, r driver.Request) ([]marshal.CommandRecord, error) {
		close(called)
		return nil, nil
	}}}
	h, err := d.Launch(context.Background(), driver.Request{Task: marshal.Task{PlanTaskID: "governed", Worker: "codex", BaseCommit: "base"}, Worktree: w.workDir, Brief: "approved instruction"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Cancel(h)
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("canonical governed runner was bypassed")
	}
	if err := d.Cancel(h); err != nil {
		t.Fatal(err)
	}
	w.tmuxMu.Lock()
	state := w.tmuxAlerts[":governed"]
	w.tmuxMu.Unlock()
	if state != "worker done: done" {
		t.Fatalf("cancel returned before notification delivery: %q", state)
	}
}
