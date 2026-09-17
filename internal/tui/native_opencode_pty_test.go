//go:build linux

package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPTYNativeOpenCodeOwnsInputAndReturnsToMarshal(t *testing.T) {
	binDir := t.TempDir()
	script := `#!/bin/sh
case "$1" in
  --version) printf '1.18.31\n'; exit 0 ;;
  session) printf '[]\n'; exit 0 ;;
esac
printf 'OPENCODE-NATIVE-READY\n'
for arg do printf 'OPENCODE-ARG:<%s>\n' "$arg"; done
IFS= read -r answer
printf 'OPENCODE-INPUT:<%s>\n' "$answer"
`
	if err := os.WriteFile(filepath.Join(binDir, "opencode"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	s := startFrozenTUICommand(t, 40, 160, "opencode", "--marshal-native-test", "path with spaces")
	s.mustSee("OPENCODE-NATIVE-READY")
	s.mustSee("OPENCODE-ARG:<path with spaces>")
	s.sendLine("FIRST-KEY-SURVIVES")
	s.mustSee("OPENCODE-INPUT:<FIRST-KEY-SURVIVES>")
	s.mustSee("OpenCode exited.")
	s.sendLine("/status")
	s.mustSee("CANONICAL STATUS DETAIL")
}
