//go:build linux

package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/statepath"
)

// Each attempt's exit is announced once under its own attempt, also when
// the workspace restarts between attempts, and no exit says the task is
// complete.
func TestTmuxAttemptBindingAcrossRestart(t *testing.T) {
	setupFakeTmuxWithDeadFile(t)
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.name", "t"}, {"config", "user.email", "t@example.com"}, {"commit", "-q", "--allow-empty", "-m", "base"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run := func(task marshal.Task) {
		w := NewWorkspace(nil, "project", "session")
		w.workDir = dir
		w.tmuxSession = "test-session"
		w.tmuxActiveWins = make(map[string]*activeTmuxAgent)
		d := &tmuxTaskDriver{w: w, inner: driver.Governed{Run: func(context.Context, driver.Request) ([]marshal.CommandRecord, error) { return nil, nil }}}
		h, err := d.Launch(context.Background(), driver.Request{RunID: "RUN-1", Task: task, Worktree: dir, Brief: "approved instruction"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.Wait(context.Background(), h); err != nil {
			t.Fatal(err)
		}
	}
	base, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	task := marshal.Task{PlanTaskID: "T-1", Worker: "codex", BaseCommit: strings.TrimSpace(string(base))}
	run(task)
	run(task) // the same attempt seen again after a restart
	task.ReturnsByAgent = map[string]int{"codex": 1}
	run(task)
	data, err := os.ReadFile(filepath.Join(dir, statepath.DirName, "inbox", "marshal.md"))
	if err != nil {
		t.Fatal(err)
	}
	inbox := string(data)
	for _, attempt := range []string{"attempt 1", "attempt 2"} {
		if !strings.Contains(inbox, "Task T-1 "+attempt+" worker exited; its hand-in waits for review.") && !strings.Contains(inbox, "Task T-1 "+attempt+" handed in") {
			t.Fatalf("%s exit missing:\n%s", attempt, inbox)
		}
	}
	if strings.Contains(inbox, "T-1 completed") || strings.Contains(inbox, "attempt 3") {
		t.Fatalf("an exit announced completion:\n%s", inbox)
	}
}
