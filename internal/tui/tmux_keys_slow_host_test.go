package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/tmux"
)

// On a slow host list-keys ran past its deadline while a worker's pane was
// being hosted, and the worker attempt failed as a Codex crash. Pane keys
// are a convenience: their failure is shown, never returned to the worker.
func TestPaneKeyBindingFailureNeverFailsTheWorker(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "tmux")
	script := "#!/bin/sh\nif [ \"$1\" = list-keys ]; then echo 'signal: killed' >&2; exit 1; fi\nexit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(fake)
	defer tmux.ResetBinaryPath()
	w := NewWorkspace(nil, "project", "session")
	w.tmuxSession = "session"
	w.tmuxMarshalPaneID = "%centre"
	if err := w.bindWorkspaceKeys(context.Background(), "%7", dir); err == nil {
		t.Fatal("fake list-keys failure did not surface from the strict binder")
	}
	w.bindWorkspaceKeysBestEffort(context.Background(), "%7", dir)
	w.mu.Lock()
	out := w.state.LastOutput
	w.mu.Unlock()
	if !strings.Contains(out, "keys for pane %7 not bound yet") {
		t.Fatalf("key failure not shown on the Board: %q", out)
	}
}
