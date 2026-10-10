package tui

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/tmux"
)

func TestTmuxF11SelectsControlAndForwardsReturnKey(t *testing.T) {
	fakeProviderCLIs(t, "codex", "claude", "opencode", "agy")
	fake, log := setupFakeTmux(t)
	w := NewWorkspace(nil, "project", "session")
	defer w.Close()
	w.tmuxPath, w.tmuxSession, w.tmuxMarshalPaneID = fake, "test-session", "%0"
	w.workDir = t.TempDir()
	for _, pane := range []string{"%0", "%1", "%2"} {
		if err := w.bindWorkspaceKeys(context.Background(), pane, w.workDir); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	bindings := 0
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "bind-key -T marshal-keys-"+tmux.ProjectHash(w.workDir)+" F11 ") {
			continue
		}
		bindings++
		if !strings.Contains(line, "select-window -t test-session:@0 ; send-keys -t %0 F11") {
			t.Fatalf("tmux F11 fails to notify the control centre: %s", line)
		}
	}
	if bindings != 3 {
		t.Fatalf("expected return bindings for control, chat and worker; got %d", bindings)
	}
}
