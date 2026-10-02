package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// useCodexByDefault pins the default provider these sweep cases exercise.
// Without it, provider-neutral commands ask which installed provider to use.
func useCodexByDefault(t *testing.T, root string) {
	t.Helper()
	if err := saveDefaultProvider(root, "codex"); err != nil {
		t.Fatal(err)
	}
}

// Install only the tools the PTY harness needs. Provider discovery can never
// find an operator's CLI, even on a machine where all four are installed.
func sweepWorkEnvironment(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"git", "go"} {
		path, err := exec.LookPath(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(path, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("PATH", dir)
	t.Chdir(t.TempDir())
	t.Setenv("MARSHAL_NO_UPDATE_CHECK", "1")
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOSUMDB", "off")
	t.Setenv("MARSHAL_CLOUD_ENDPOINT", "off")
	InvalidateProbeCache()
	t.Cleanup(InvalidateProbeCache)
}

func sweepAgentExecute(t *testing.T, ws *Workspace, line string) string {
	t.Helper()
	out, err := ws.ExecuteCommand(context.Background(), line)
	if err != nil {
		out += " " + err.Error()
	}
	if strings.TrimSpace(out) == "" {
		t.Fatalf("%s silently did nothing", line)
	}
	return out
}
