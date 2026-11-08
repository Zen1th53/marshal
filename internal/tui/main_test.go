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
	// The navigation surface is closed to users until it is verified; its
	// own tests still exercise it. The PTY tests run the real binary, where
	// it stays closed.
	navigationReleased = true
	if os.Getenv("MARSHAL_CLOUD_ENDPOINT") == "" {
		_ = os.Setenv("MARSHAL_CLOUD_ENDPOINT", "off")
	}
	if os.Getenv("MARSHAL_NO_UPDATE_CHECK") == "" {
		_ = os.Setenv("MARSHAL_NO_UPDATE_CHECK", "1")
	}
	// Never reach a person's tmux server. The PTY and tmux tests start real
	// sessions; a worker or developer running this suite inside Marshal (or
	// any tmux) would otherwise create and respawn panes in that live server.
	_ = os.Unsetenv("TMUX")
	_ = os.Unsetenv("TMUX_PANE")
	// A short base keeps the tmux socket path under the Unix socket limit.
	tmuxDir, err := os.MkdirTemp("/tmp", "mtt-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "TUI test tmux isolation failed:", err)
		os.Exit(1)
	}
	_ = os.Setenv("TMUX_TMPDIR", tmuxDir)
	cleanup, err := isolateOrdinaryTestProviderPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, "TUI test provider isolation failed:", err)
		os.Exit(1)
	}
	code := m.Run()
	cleanup()
	_ = os.RemoveAll(tmuxDir)
	os.Exit(code)
}
