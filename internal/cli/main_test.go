package cli

import (
	"context"
	"fmt"
	"github.com/Zen1th53/marshal/internal/tmux"
	"os"
	"testing"
)

// TestMain keeps tests off the production Community Cloud: commands that open
// a session would otherwise register a new installation with the live service
// from every scratch project. A test that needs a Cloud sets its own endpoint.
func TestMain(m *testing.M) {
	// Subprocess fixture for the real tmux child command line.
	if os.Getenv("MARSHAL_TEST_TUI_PROCESS") == "1" {
		tmux.SetBinaryPath(os.Getenv("MARSHAL_TEST_TMUX_BINARY"))
		os.Exit(Run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
	}
	if os.Getenv("MARSHAL_CLOUD_ENDPOINT") == "" {
		_ = os.Setenv("MARSHAL_CLOUD_ENDPOINT", "off")
	}
	config, err := os.MkdirTemp("", "marshal-cli-config-")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("XDG_CONFIG_HOME", config)
	cleanup, err := isolateOrdinaryTestProviderPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, "CLI test provider isolation failed:", err)
		os.Exit(1)
	}
	code := m.Run()
	cleanup()
	_ = os.RemoveAll(config)
	os.Exit(code)
}
