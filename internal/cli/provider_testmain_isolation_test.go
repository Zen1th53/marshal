package cli

import (
	"os"
	"os/exec"
	"testing"

	"github.com/Zen1th53/marshal/internal/project"
)

func TestCLIOrdinarySuiteCannotDiscoverOperatorProviders(t *testing.T) {
	if os.Getenv("MARSHAL_REAL_MODEL") == "1" {
		t.Skip("explicit live-model run")
	}
	for _, name := range []string{"codex", "claude", "opencode", "agy", "antigravity", "gemini"} {
		if path, err := project.FindBinary(name); err == nil {
			t.Errorf("ordinary CLI test can discover %s at %s", name, path)
		}
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatal("provider isolation removed the non-provider git tool:", err)
	}
}
