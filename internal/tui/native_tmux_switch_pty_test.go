//go:build linux

package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPTYSwitchTmuxAgentsWithoutRestart tests that in a tmux environment,
// opening two native agents and switching back and forth does not kill or restart either agent.
func TestPTYSwitchTmuxAgentsWithoutRestart(t *testing.T) {
	binDir := t.TempDir()
	logFile := filepath.Join(binDir, "tmux_argv.log")
	winFile := filepath.Join(binDir, "tmux_windows.log")

	fakeTmux := fmt.Sprintf(`#!/bin/sh
echo "$@" >> %q
winFile=%q
case "$1" in
  -V)
    printf 'tmux 3.3a\n'
    exit 0
    ;;
  new-window)
    prev=""
    for a in "$@"; do
      if [ "$prev" = "-n" ]; then
        echo "$a" >> "$winFile"
      fi
      prev="$a"
    done
    exit 0
    ;;
  display-message)
    case "$*" in
      *"#{version}"*)
        printf '3.3a\n'
        exit 0
        ;;
      *"#{session_id}"*)
        printf '$0\n'
        exit 0
        ;;
      *"#{session_name}"*"#{window_name}"*"#{window_id}"*)
        printf 'test-session\tmarshal\t@0\n'
        exit 0
        ;;
      *"#{pane_id}"*)
        printf '%%%%0\n'
        exit 0
        ;;
      *"#{pane_dead}"*)
        printf '0\n'
        exit 0
        ;;
    esac
    exit 0
    ;;
  list-panes)
    printf '%%%%0\t@0\tmarshal\t100\t0\t0\n'
    if [ -f "$winFile" ]; then
      idx=1
      while read -r w; do
        echo "%%%%$idx	@$idx	$w	$((100 + idx))	0	0	"
        idx=$((idx + 1))
      done < "$winFile"
    fi
    exit 0
    ;;
  list-windows)
    printf 'marshal\n'
    if [ -f "$winFile" ]; then
      cat "$winFile"
    fi
    exit 0
    ;;
  capture-pane)
    printf 'fake output evidence\n'
    exit 0
    ;;
  *)
    exit 0
    ;;
esac
`, logFile, winFile)

	agentScript := `#!/bin/sh
for arg do
  case "$arg" in
    --version|-v) printf '1.0.0\n'; exit 0 ;;
  esac
done
echo session-started
while :; do sleep 1; done
`

	if err := os.WriteFile(filepath.Join(binDir, "tmux"), []byte(fakeTmux), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "codex"), []byte(agentScript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte(agentScript), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX", "/tmp/tmux-test,1234,0")

	s := startFrozenTUI(t, 40, 160)
	s.mustSee("MARSHAL")

	// 1. Open Claude in tmux
	s.sendLine("/claude")
	s.mustSee("Opened native Claude in tmux window")
	s.mustSee("view-only mode")
	s.mustSee("F11 to return to MARSHAL")

	// 2. Open Codex in tmux
	s.sendLine("/codex")
	s.mustSee("Opened native Codex in tmux window")

	// 3. Switch back to Claude (should NOT restart, must switch)
	s.sendLine("/claude")
	s.mustSee("Switched to active Claude session")
	s.mustSee("continues running")

	// 4. Switch back to Codex (should NOT restart, must switch)
	s.sendLine("/codex")
	s.mustSee("Switched to active Codex session")
	s.mustSee("continues running")

	// 5. Verify Stop all workers
	s.sendLine("/stop all")
	s.mustSee("Stopped all worker sessions")
	s.mustSee("MARSHAL remains active")

	// Check the fake tmux log
	logData, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	logStr := string(logData)

	// Verify new-window was only called ONCE for claude and ONCE for codex
	claudeNewCount := strings.Count(logStr, "new-window -t test-session -n marshal-claude-")
	codexNewCount := strings.Count(logStr, "new-window -t test-session -n marshal-codex-")

	if claudeNewCount != 1 {
		t.Errorf("expected claude new-window called exactly once, got %d", claudeNewCount)
	}
	if codexNewCount != 1 {
		t.Errorf("expected codex new-window called exactly once, got %d", codexNewCount)
	}

	// Verify select-window was called for switching
	if !strings.Contains(logStr, "select-window -t test-session:marshal-claude-") {
		t.Errorf("expected select-window for claude in log:\n%s", logStr)
	}
	if !strings.Contains(logStr, "select-window -t test-session:marshal-codex-") {
		t.Errorf("expected select-window for codex in log:\n%s", logStr)
	}
}
