//go:build linux

package tui

import (
	"fmt"
	"github.com/Zen1th53/marshal/internal/statepath"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPTYSwitchTmuxAgentsWithoutRestart tests that in a tmux environment,
// opening two native agents and switching back and forth does not kill or restart either agent.
func TestPTYSwitchTmuxAgentsWithoutRestart(t *testing.T) {
	bin := buildMarshalBinary(t)
	project := initProject(t, bin)
	session := fixtureTmuxNamespace(t, project)
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
    case " $* " in *" -P "*) printf '%%%%%%s\n' "$(wc -l < "$winFile")" ;; esac
    exit 0
    ;;
  display-message)
    case "$*" in
      *"#{pane_height} #{pane_width}"*) printf '40 160\n'; exit 0;;
      *"#{version}"*)
        printf '3.3a\n'
        exit 0
        ;;
      *"#{session_name}:#{window_id}"*)
        printf 'test-session:@%%s\n' "${4#%%}"
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
        case "$4" in %%*) printf '%%s\n' "$4" ;; *) printf '%%%%0\n' ;; esac
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
        echo "%%$idx	@$idx	$w	$((100 + idx))	0	0	"
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
	fakeTmux = strings.ReplaceAll(fakeTmux, "test-session", session)
	fakeTmux = strings.ReplaceAll(fakeTmux, "\tmarshal\t", "\t"+statepath.Name+"\t")
	fakeTmux = strings.ReplaceAll(fakeTmux, "\tmarshal\n", "\t"+statepath.Name+"\n")
	fakeTmux = strings.ReplaceAll(fakeTmux, "\\tmarshal\\t", "\\t"+statepath.Name+"\\t")
	fakeTmux = strings.ReplaceAll(fakeTmux, "printf 'marshal\\n'", "printf '"+statepath.Name+"\\n'")

	// The double answers startup probes (version, help, model catalog) and
	// stays alive only for the interactive session. A probe that fell through
	// to the session loop would hang MARSHAL's bounded probe until its timeout
	// and leak the governed subprocess.
	agentScript := `#!/bin/sh
for arg do
  case "$arg" in
    --version|-v) printf '1.0.0\n'; exit 0 ;;
    login|--help|debug) exit 0 ;;
  esac
done
# A model inventory probe must not turn into an immortal fake session.
if [ "$1" = debug ] && [ "$2" = models ]; then printf '[]\n'; exit 0; fi
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

	s := startFrozenTUIInProject(t, 40, 160, bin, project, "tui")
	s.mustSee("MARSHAL")

	// 1. Open Claude in tmux
	s.sendLine("/claude")
	s.mustSee("Opened native Claude in tmux window")
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

	// Explicit provider F-keys must still switch without restarting either session.
	s.send("\x1b[18~") // F7: Codex
	s.mustSee("Switched to active Codex session")
	s.send("\x1b[19~") // F8: Claude
	s.mustSee("Switched to active Claude session")

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
	paneByProvider := make(map[string]string)
	windowByProvider := make(map[string]string)
	index := 0
	for _, line := range strings.Split(logStr, "\n") {
		if strings.HasPrefix(line, "new-window ") {
			index++
			for _, provider := range []string{"claude", "codex"} {
				if strings.Contains(line, "-n "+statepath.Name+"-"+provider+"-") {
					paneByProvider[provider] = fmt.Sprintf("%%%d", index)
					windowByProvider[provider] = fmt.Sprintf("%s:@%d", session, index)
				}
			}
		}
	}
	for _, provider := range []string{"claude", "codex"} {
		enabled := false
		for _, line := range strings.Split(logStr, "\n") {
			if strings.HasPrefix(line, "select-pane -t "+paneByProvider[provider]+" ") {
				if strings.HasSuffix(line, " -d") {
					t.Fatalf("operator %s pane opened view-only: %s", provider, line)
				}
				enabled = enabled || strings.HasSuffix(line, " -e")
			}
		}
		if !enabled {
			t.Fatalf("operator %s pane input was not enabled:\n%s", provider, logStr)
		}
	}

	// Verify new-window was only called ONCE for claude and ONCE for codex
	claudeNewCount := strings.Count(logStr, "new-window -d -t "+session+" -n "+statepath.Name+"-claude-")
	codexNewCount := strings.Count(logStr, "new-window -d -t "+session+" -n "+statepath.Name+"-codex-")

	if claudeNewCount != 1 {
		t.Errorf("expected claude new-window called exactly once, got %d", claudeNewCount)
	}
	if codexNewCount != 1 {
		t.Errorf("expected codex new-window called exactly once, got %d", codexNewCount)
	}

	// Verify select-window was called for switching
	if !strings.Contains(logStr, "select-window -t "+windowByProvider["claude"]) {
		t.Errorf("expected select-window for claude in log:\n%s", logStr)
	}
	if !strings.Contains(logStr, "select-window -t "+windowByProvider["codex"]) {
		t.Errorf("expected select-window for codex in log:\n%s", logStr)
	}
}
