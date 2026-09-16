//go:build linux

package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPTYSwitchBetweenProviders drives the real switch: open one agent, leave it,
// open the other, and come back. Each session must launch its own binary, and the
// second must be told what the first did — which is the whole point of switching
// inside MARSHAL rather than running the two CLIs side by side.
func TestPTYSwitchBetweenProviders(t *testing.T) {
	binDir := t.TempDir()
	claudeHome, codexHome := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(claudeHome, "projects"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(codexHome, "sessions"), 0700); err != nil {
		t.Fatal(err)
	}

	// Each double answers the version probe, reports the argv it was handed, takes
	// one line and writes the history its real CLI would leave behind.
	claude := `#!/bin/sh
for arg do
  case "$arg" in
    --version|-v) printf '2.0.0 (Claude Code)\n'; exit 0 ;;
  esac
done
printf 'CLAUDE-READY\n'
for arg do printf 'CLAUDE-ARG:<%s>\n' "$arg"; done
IFS= read -r answer
printf 'CLAUDE-INPUT:<%s>\n' "$answer"
cp "$MARSHAL_TEST_CLAUDE_HISTORY" "$CLAUDE_CONFIG_DIR/projects/session.jsonl"
`
	codex := `#!/bin/sh
for arg do
  case "$arg" in
    --version|-v) printf 'codex-cli 0.1.0\n'; exit 0 ;;
  esac
done
printf 'CODEX-READY\n'
for arg do printf 'CODEX-ARG:<%s>\n' "$arg"; done
IFS= read -r answer
printf 'CODEX-INPUT:<%s>\n' "$answer"
cp "$MARSHAL_TEST_CODEX_HISTORY" "$CODEX_HOME/sessions/rollout.jsonl"
`
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte(claude), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "codex"), []byte(codex), 0700); err != nil {
		t.Fatal(err)
	}

	claudeHistory := filepath.Join(claudeHome, "fixture.jsonl")
	codexHistory := filepath.Join(codexHome, "fixture.jsonl")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CLAUDE_CONFIG_DIR", claudeHome)
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("MARSHAL_TEST_CLAUDE_HISTORY", claudeHistory)
	t.Setenv("MARSHAL_TEST_CODEX_HISTORY", codexHistory)

	s := startFrozenTUI(t, 40, 160)
	s.mustSee("MARSHAL")

	project := s.cmd.Dir
	if resolved, err := filepath.EvalSymlinks(project); err == nil {
		project = resolved
	}
	if err := os.WriteFile(claudeHistory, nativeClaudeHistory(t, project, "switch-claude"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexHistory, nativeTestHistory(t, project, "switch-codex"), 0600); err != nil {
		t.Fatal(err)
	}

	// F8 opens Claude.
	s.send("\x1b[19~")
	s.mustSee("CLAUDE-READY")
	s.sendLine("first")
	s.mustSee("CLAUDE-INPUT:<first>")
	s.mustSee("Claude exited.")

	// F7 switches to Codex, which must be a different binary, not the last one
	// reopened. Its briefing carries what Claude just did.
	s.send("\x1b[18~")
	s.mustSee("CODEX-READY")
	s.mustSee("MARSHAL cross-agent memory")
	s.sendLine("second")
	s.mustSee("CODEX-INPUT:<second>")
	s.mustSee("Codex exited.")

	// And back again, with Codex's work now in Claude's briefing.
	s.send("\x1b[19~")
	s.mustSee("CLAUDE-READY")
	s.sendLine("third")
	s.mustSee("CLAUDE-INPUT:<third>")
	s.mustSee("Claude exited.")

	out := s.output()
	// Each provider must have been launched on its own, never both at once: a
	// native session owns the terminal for as long as it runs.
	if got := strings.Count(out, "CLAUDE-READY"); got != 2 {
		t.Errorf("Claude opened %d time(s), want 2", got)
	}
	if got := strings.Count(out, "CODEX-READY"); got != 1 {
		t.Errorf("Codex opened %d time(s), want 1", got)
	}

	// The slash commands are the other route to the same switch.
	s.sendLine("/codex cli --marshal-switch-test")
	s.mustSee("CODEX-ARG:<--marshal-switch-test>")
	s.sendLine("fourth")
	s.mustSee("CODEX-INPUT:<fourth>")
}
