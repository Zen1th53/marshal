package tui

import (
	"os"
	"path/filepath"
	"testing"
)

// This fixture observes only owned host scripts, never desktop provider CLIs.
func isolateRequiredFixtureProviders(t *testing.T) {
	t.Helper()
	// Restore the original environment before clearing discovery results.
	t.Cleanup(InvalidateProbeCache)
	bin := t.TempDir()
	for _, name := range []string{"codex", "claude", "opencode", "agy", "antigravity"} {
		script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo '" + name + " fixture-test'; exit 0; fi\nexit 127\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	InvalidateProbeCache()
}
