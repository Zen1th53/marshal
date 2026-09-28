package cli

import (
	"os"
	"testing"
)

// TestMain keeps tests off the production Community Cloud: commands that open
// a session would otherwise register a new installation with the live service
// from every scratch project. A test that needs a Cloud sets its own endpoint.
func TestMain(m *testing.M) {
	if os.Getenv("MARSHAL_CLOUD_ENDPOINT") == "" {
		_ = os.Setenv("MARSHAL_CLOUD_ENDPOINT", "off")
	}
	os.Exit(m.Run())
}
