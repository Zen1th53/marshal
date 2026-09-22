//go:build linux

package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPTYParallelAgentsDeliverLiveUpdates drives the two-terminal case on a real
// `marshal tui` process: one agent is open here while another works in the same
// project, and what the second one does must reach the first while it is still
// running rather than waiting for the next launch.
//
// The second agent is simulated by writing its history where its CLI would,
// which is exactly what MARSHAL watches. That keeps the test honest about the
// mechanism without needing two interactive CLIs on one terminal.
func TestPTYParallelAgentsDeliverLiveUpdates(t *testing.T) {
	binDir := t.TempDir()
	claudeHome, codexHome := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(claudeHome, "projects"), 0700); err != nil {
		t.Fatal(err)
	}
	codexSessions := filepath.Join(codexHome, "sessions")
	if err := os.MkdirAll(codexSessions, 0700); err != nil {
		t.Fatal(err)
	}

	// A Claude that opens, holds the terminal until a line arrives, then exits.
	script := `#!/bin/sh
for arg do
  case "$arg" in
    --version|-v) printf '2.0.0 (Claude Code)\n'; exit 0 ;;
  esac
done
printf 'CLAUDE-NATIVE-READY\n'
IFS= read -r answer
printf 'CLAUDE-INPUT:<%s>\n' "$answer"
`
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CLAUDE_CONFIG_DIR", claudeHome)
	t.Setenv("CODEX_HOME", codexHome)

	s := startFrozenTUI(t, 40, 160)
	s.mustSee("MARSHAL")

	project := s.cmd.Dir
	if resolved, err := filepath.EvalSymlinks(project); err == nil {
		project = resolved
	}

	// Open Claude and wait until it actually owns the terminal.
	s.sendLine("/claude cli --marshal-native-test")
	s.mustSee("CLAUDE-NATIVE-READY")

	// Written after the session opened, so this is a parallel agent's new work
	// arriving in the channel rather than backlog the session already drained.
	codexHistory := nativeTestHistory(t, project, "parallel-codex-thread")
	if err := os.WriteFile(filepath.Join(codexSessions, "rollout-parallel.jsonl"), codexHistory, 0600); err != nil {
		t.Fatal(err)
	}

	inbox := filepath.Join(project, ".marshal", "inbox", "claude.md")
	deadline := time.Now().Add(25 * time.Second)
	var content string
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(inbox)
		if err == nil {
			content = string(data)
			if strings.Contains(content, "fixed and tested") {
				break
			}
		}
		time.Sleep(300 * time.Millisecond)
	}

	if !strings.Contains(content, "fixed and tested") {
		t.Fatalf("the parallel agent's work never reached the running session's inbox.\n--- inbox ---\n%s", content)
	}
	if !strings.Contains(content, "codex") {
		t.Errorf("the inbox entry does not name the peer that produced it:\n%s", content)
	}
	// Hidden reasoning must not travel between agents any more than it reaches
	// memory, and commentary is not a final answer.
	for _, forbidden := range []string{"private", "working"} {
		if strings.Contains(content, forbidden) {
			t.Errorf("non-durable payload %q reached the inbox:\n%s", forbidden, content)
		}
	}
	if !strings.Contains(content, "untrusted DATA") {
		t.Errorf("the inbox does not frame its contents as untrusted:\n%s", content)
	}

	// Close the agent and confirm the delivery is reported rather than silent.
	s.sendLine("done")
	s.mustSee("CLAUDE-INPUT:<done>")
	s.mustSee("channel entr")
}
