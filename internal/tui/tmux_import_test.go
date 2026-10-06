package tui

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

func TestImportedTaskDoesNotRegisterOrClosePane(t *testing.T) {
	_, log := setupFakeTmux(t)
	repo := testgit.New(t)
	w := NewWorkspace(nil, "project", "session")
	w.workDir = repo.Path()
	req := driver.Request{RunID: "import", Worktree: repo.Path(), Brief: "check result", Task: marshal.Task{PlanTaskID: "imported", Worker: "test", BaseCommit: repo.HEAD(t), ImportedResult: &marshal.ImportedResult{TaskID: "TASK-cli"}}}
	d := &tmuxTaskDriver{w: w, inner: driver.Governed{Run: func(context.Context, driver.Request) ([]marshal.CommandRecord, error) { return nil, nil }}}
	h, err := d.Launch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	w.tmuxMu.Lock()
	count := len(w.tmuxActiveWins)
	w.tmuxMu.Unlock()
	if count != 0 {
		t.Errorf("unhosted task created %d pane records", count)
	}
	if _, err = d.Wait(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	commands, err := os.ReadFile(log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if strings.Contains(string(commands), "capture-pane") || strings.Contains(string(commands), "kill-pane") || strings.Contains(string(commands), "new-window") {
		t.Fatalf("import touched panes: %s", commands)
	}
}

func TestPaneLessCleanupAndTakeoverRefuseTmuxDispatch(t *testing.T) {
	path, log := setupFakeTmux(t)
	t.Setenv("MARSHAL_TEST_FORCE_TMUX", "1")
	w := NewWorkspace(nil, "project", "session")
	w.workDir = t.TempDir()
	w.tmuxPath = path
	a := &activeTmuxAgent{id: "task-old", role: "task", paneID: ""}
	if err := w.retainAndCloseAgent(context.Background(), a, w.workDir, "completed"); err == nil {
		t.Fatal("cleanup accepted missing pane")
	}
	w.tmuxActiveWins[a.id] = a
	if _, err := w.handleTakeoverCommand(context.Background()); err == nil {
		t.Fatal("takeover accepted missing pane")
	}
	commands, err := os.ReadFile(log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(commands) != 0 {
		t.Fatalf("missing pane reached tmux: %s", commands)
	}
}
