package tui

import (
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
	os.Exit(m.Run())
}
