//go:build linux

package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPTYNativeClaudeInputMemoryAndContinue(t *testing.T) {
	binDir, config := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(config, "projects", "fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
case "$1" in
  --marshal-native-test|--continue|--resume|--|'') ;;
  *) printf '2.0.0 (Claude Code)\n'; exit 0 ;;
esac
printf 'CLAUDE-NATIVE-READY\n'
for arg do printf 'CLAUDE-ARG:<%s>\n' "$arg"; done
IFS= read -r answer
printf 'CLAUDE-INPUT:<%s>\n' "$answer"
cp "$MARSHAL_TEST_CLAUDE_HISTORY" "$CLAUDE_CONFIG_DIR/projects/fixture/session.jsonl"
`
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	history := filepath.Join(config, "fixture.jsonl")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	t.Setenv("MARSHAL_TEST_CLAUDE_HISTORY", history)
	s := startTUI(t, 40, 160)
	if err := os.WriteFile(history, nativeClaudeHistory(t, s.cmd.Dir, "claude-pty"), 0600); err != nil {
		t.Fatal(err)
	}
	// Bare `/claude`, with no subcommand, must open the native session. That is
	// the form operators actually type, and it is the one that matches the
	// provider's own invocation name, so it is asserted before the flag-bearing
	// variants below.
	s.sendLine("/claude")
	s.mustSee("CLAUDE-NATIVE-READY")
	s.sendLine("BARE-CLAUDE")
	s.mustSee("CLAUDE-INPUT:<BARE-CLAUDE>")

	s.sendLine(`/claude cli --marshal-native-test --add-dir "directory with spaces"`)
	s.mustSee("CLAUDE-ARG:<directory with spaces>")
	s.sendLine("FIRST-KEY")
	s.mustSee("CLAUDE-INPUT:<FIRST-KEY>")
	s.mustSee("Claude exited. 2 conversation messages saved")
	s.sendLine("/claude continue")
	s.mustSee("CLAUDE-ARG:<--continue>")
	s.sendLine("CONTINUED")
	s.mustSee("CLAUDE-INPUT:<CONTINUED>")
	s.mustSee("Claude exited. 0 conversation messages saved")
	s.sendLine("/memory search visible")
	s.mustSee("MEMORY RECORDS (2 of 2)")
	s.sendLine("another prompt")
	s.mustSee("CLAUDE-ARG:<another prompt>")
	s.sendLine("PLAIN-PROMPT-CLAUDE")
	s.mustSee("CLAUDE-INPUT:<PLAIN-PROMPT-CLAUDE>")
	s.send("\x1b")
	s.send("\x1b[19~") // F8 from navigation opens native Claude.
	s.sendLine("F8-CLAUDE")
	s.mustSee("CLAUDE-INPUT:<F8-CLAUDE>")
}

func TestPTYInstalledNativeClaudeHelp(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("Claude CLI not installed")
	}
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	s := startFrozenTUICommand(t, 40, 160, "claude", "--help")
	s.mustSee("--permission-mode")
	s.mustSee("Claude exited.")
	s.sendLine("/status")
	s.mustSee("CANONICAL STATUS DETAIL")
}
