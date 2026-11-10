package tui

import (
	"fmt"
	"os"
	"testing"
)

// TestMain keeps tests off the production Community Cloud. The PTY tests run
// the real binary in scratch projects and it inherits this environment; with
// the default endpoint each of them registered a new installation with the
// live service. A test that needs a Cloud sets its own endpoint.
func TestMain(m *testing.M) {
	if os.Getenv("MARSHAL_CLOUD_ENDPOINT") == "" {
		_ = os.Setenv("MARSHAL_CLOUD_ENDPOINT", "off")
	}
	if os.Getenv("MARSHAL_NO_UPDATE_CHECK") == "" {
		_ = os.Setenv("MARSHAL_NO_UPDATE_CHECK", "1")
	}
	// Never reach a person's tmux server. The PTY and tmux tests start real
	// sessions; a worker or developer running this suite inside Marshal (or
	// any tmux) would otherwise create and respawn panes in that live server.
	// Helper processes re-run this binary and inherit the parent's directory.
	_ = os.Unsetenv("TMUX")
	_ = os.Unsetenv("TMUX_PANE")
	tmuxDir := ""
	if os.Getenv("MARSHAL_TUI_TEST_TMUX") == "" {
		// A short base keeps the tmux socket path under the Unix socket limit.
		dir, err := os.MkdirTemp("/tmp", "mtt-")
		if err != nil {
			fmt.Fprintln(os.Stderr, "TUI test tmux isolation failed:", err)
			os.Exit(1)
		}
		tmuxDir = dir
		_ = os.Setenv("MARSHAL_TUI_TEST_TMUX", dir)
		_ = os.Setenv("TMUX_TMPDIR", dir)
	}
	cleanup, err := isolateOrdinaryTestProviderPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, "TUI test provider isolation failed:", err)
		os.Exit(1)
	}
	code := m.Run()
	cleanup()
	if tmuxDir != "" {
		_ = os.RemoveAll(tmuxDir)
	}
	os.Exit(code)
}
