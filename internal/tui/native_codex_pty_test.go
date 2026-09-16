//go:build linux

package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Exercise the compiled host and a real controlling terminal. A mock authority
// cannot catch a competing read stealing the child's first keystroke.
func TestPTYNativeCodexOwnsInputAndSavesMemory(t *testing.T) {
	binDir, home := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "sessions"), 0700); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
case "$1" in
  --marshal-native-test) ;;
  *) printf 'codex-cli 0.1.0\n'; exit 0 ;;
esac
shift
printf 'NATIVE-READY\n'
for arg do printf 'ARG:<%s>\n' "$arg"; done
IFS= read -r answer
printf 'NATIVE-INPUT:<%s>\n' "$answer"
cp "$MARSHAL_TEST_HISTORY" "$CODEX_HOME/sessions/rollout.jsonl"
exit 0
`
	if err := os.WriteFile(filepath.Join(binDir, "codex"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	history := filepath.Join(home, "fixture.jsonl")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CODEX_HOME", home)
	t.Setenv("MARSHAL_TEST_HISTORY", history)
	s := startTUI(t, 40, 160)
	if err := os.WriteFile(history, nativeTestHistory(t, s.cmd.Dir, "native-pty-session"), 0600); err != nil {
		t.Fatal(err)
	}
	s.sendLine(`/codex cli --marshal-native-test -i "picture with spaces.png"`)
	s.mustSee("NATIVE-READY")
	s.mustSee("ARG:<picture with spaces.png>")
	s.sendLine("FIRST-KEY-SURVIVES")
	s.mustSee("NATIVE-INPUT:<FIRST-KEY-SURVIVES>")
	s.mustSee("2 message(s), including tool calls, saved to MARSHAL memory")
	// Returning to MARSHAL must reclaim input and leave commands usable.
	s.sendLine("/status")
	s.mustSee("CANONICAL STATUS DETAIL")
	s.sendLine(`/codex cli --marshal-native-test`)
	s.sendLine("SECOND-SESSION")
	s.mustSee("NATIVE-INPUT:<SECOND-SESSION>")
	s.mustSee("0 message(s), including tool calls, saved to MARSHAL memory")
}

func TestPTYInstalledNativeCodexHelp(t *testing.T) {
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("Codex CLI not installed")
	}
	// Verify the actual installed CLI without touching account history or
	// sending a prompt to a paid model.
	t.Setenv("CODEX_HOME", t.TempDir())
	s := startTUI(t, 40, 160)
	s.sendLine("/codex cli --help")
	s.mustSee("Codex CLI")
	s.mustSee("Codex exited.")
	s.sendLine("/status")
	s.mustSee("CANONICAL STATUS DETAIL")
}
