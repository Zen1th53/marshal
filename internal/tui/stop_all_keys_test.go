package tui

import (
	"context"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"os"
	"strings"
	"testing"
)

func TestStopAllKeyTargetsControlFromEveryMarshalPane(t *testing.T) {
	fake, log := setupFakeTmux(t)
	w := NewWorkspace(nil, "project", "session")
	w.tmuxSession = "owned-session"
	w.tmuxPath = fake
	w.workDir = t.TempDir()
	t.Setenv("MARSHAL_TEST_FORCE_TMUX", "1")
	// Use real tmux ID syntax: Marshal %0, its chat %1, and a worker %2.
	w.tmuxMarshalPaneID = "%0"
	for _, pane := range []string{"%0", "%1", "%2"} {
		if err := w.bindWorkspaceKeys(context.Background(), pane, w.workDir); err != nil {
			t.Fatal(err)
		}
	}
	// Exercise the stop target without launching a provider or process supervisor.
	cancelled := false
	handle := driver.NewHandle(driver.Request{})
	handle.SetCancel(func() { cancelled = true })
	handle.Complete()
	chat := &activeTmuxAgent{id: "marshal-chat", role: "marshal-chat", paneID: "%1"}
	w.tmuxActiveWins = map[string]*activeTmuxAgent{
		"worker":       {id: "worker", role: "worker", label: "Worker", paneID: "%2", driver: driver.Native{}, handle: handle},
		"marshal-chat": chat,
	}
	response, err := w.cmd.Handle(context.Background(), "/stop all")
	if err != nil || !cancelled || !strings.Contains(response, "MARSHAL remains active") {
		t.Fatalf("stop target: %s %v", response, err)
	}
	if len(w.tmuxActiveWins) != 1 || w.tmuxActiveWins["marshal-chat"] != chat {
		t.Fatal("stop removed Marshal or retained worker")
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "C-x if-shell") != 3 || strings.Count(string(data), "send-keys -t %0 C-x") != 6 {
		t.Fatalf("stop key did not route to control: %s", data)
	}
	if !strings.Contains(string(data), "kill-pane -t %2") || strings.Contains(string(data), "kill-pane -t %1") || strings.Contains(string(data), "kill-pane -t %0") {
		t.Fatalf("wrong panes stopped: %s", data)
	}
	if strings.Contains(string(data), "/stop all") {
		t.Fatal("stop key typed into a composer")
	}
}
