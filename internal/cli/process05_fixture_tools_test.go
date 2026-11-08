package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// Explicit metadata-only provider doubles prevent these lifecycle fixtures
// from discovering an operator's CLIs. Unexpected task execution is a fixture
// failure; no successful native model or live version observation is claimed.
func installProcess05FixtureTools(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	marker := filepath.Join(bin, "unexpected-invocation")
	script := "#!/bin/sh\ncase \"$1\" in --version) echo 1.0.0;; --help) echo owned-process05-metadata-fixture;; *) printf '%s\\n' unexpected >> \"$MARSHAL_PROCESS05_OWNED_UNEXPECTED\"; exit 97;; esac\n"
	for _, name := range []string{"codex", "claude", "opencode", "agy", "antigravity", "gemini"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	t.Setenv("MARSHAL_PROCESS05_OWNED_UNEXPECTED", marker)
	t.Cleanup(func() {
		if b, err := os.ReadFile(marker); err == nil && len(b) > 0 {
			t.Errorf("lifecycle fixture attempted unexpected provider command: %s", b)
		} else if err != nil && !os.IsNotExist(err) {
			t.Error(err)
		}
	})
}
