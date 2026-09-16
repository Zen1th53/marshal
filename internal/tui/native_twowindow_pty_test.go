//go:build linux

package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// startInProject launches `marshal tui` against an existing repository, so two
// sessions can share one project the way two terminals do.
func startInProject(t *testing.T, project string, rows, cols uint16, args ...string) *ptySession {
	t.Helper()
	bin := buildMarshalBinary(t)
	master, slave := openPTY(t)
	setWinsize(master, rows, cols)

	cmd := exec.Command(bin, args...)
	cmd.Dir = project
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start marshal in %s: %v", project, err)
	}
	slave.Close()

	s := &ptySession{t: t, master: master, cmd: cmd}
	go func() {
		chunk := make([]byte, 8192)
		for {
			n, err := master.Read(chunk)
			if n > 0 {
				s.mu.Lock()
				s.buf.Write(chunk[:n])
				s.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	})
	return s
}

// TestPTYTwoWindowsShareOneProjectMemory runs the arrangement an operator
// actually reaches for: two terminals on one repository, a different agent in
// each, at the same time.
//
// A native session holds its own terminal, so two agents can only run together
// like this. What must hold is that they are not two disconnected worlds: the
// project database is one, and either window can see what both agents did.
func TestPTYTwoWindowsShareOneProjectMemory(t *testing.T) {
	binDir := t.TempDir()
	claudeHome, codexHome := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(claudeHome, "projects"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(codexHome, "sessions"), 0700); err != nil {
		t.Fatal(err)
	}

	double := func(name, marker, historyEnv, dest string) {
		script := "#!/bin/sh\n" +
			"for arg do case \"$arg\" in --version|-v) printf 'stub 0.1\\n'; exit 0 ;; esac; done\n" +
			"printf '" + marker + "-READY\\n'\n" +
			"IFS= read -r line\n" +
			"printf '" + marker + "-IN:<%s>\\n' \"$line\"\n" +
			"cp \"$" + historyEnv + "\" \"" + dest + "\"\n" +
			"IFS= read -r done_line\n"
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	double("claude", "CLAUDE", "MARSHAL_TEST_CLAUDE_HISTORY", filepath.Join(claudeHome, "projects", "session.jsonl"))
	double("codex", "CODEX", "MARSHAL_TEST_CODEX_HISTORY", filepath.Join(codexHome, "sessions", "rollout.jsonl"))

	claudeHistory := filepath.Join(claudeHome, "fixture.jsonl")
	codexHistory := filepath.Join(codexHome, "fixture.jsonl")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CLAUDE_CONFIG_DIR", claudeHome)
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("MARSHAL_TEST_CLAUDE_HISTORY", claudeHistory)
	t.Setenv("MARSHAL_TEST_CODEX_HISTORY", codexHistory)

	project := initProject(t, buildMarshalBinary(t))
	resolved := project
	if r, err := filepath.EvalSymlinks(project); err == nil {
		resolved = r
	}
	if err := os.WriteFile(claudeHistory, nativeClaudeHistory(t, resolved, "two-window-claude"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexHistory, nativeTestHistory(t, resolved, "two-window-codex"), 0600); err != nil {
		t.Fatal(err)
	}

	// Neither window passes --session, so both take the default identity.
	first := startInProject(t, project, 40, 160)
	second := startInProject(t, project, 40, 160)
	first.mustSee("MARSHAL")
	second.mustSee("MARSHAL")

	first.sendLine("/claude")
	first.mustSee("CLAUDE-READY")
	second.sendLine("/codex")
	second.mustSee("CODEX-READY")

	// Both agents are live in the same repository at once.
	first.sendLine("alpha")
	first.mustSee("CLAUDE-IN:<alpha>")
	second.sendLine("beta")
	second.mustSee("CODEX-IN:<beta>")

	// Give both watchers a poll to import what the doubles wrote.
	time.Sleep(5 * time.Second)

	// Leave both agents and ask one window what the project remembers. It must
	// carry both agents' work, not only the one this window opened.
	first.send("\r")
	second.send("\r")
	first.mustSee("Claude exited.")

	first.sendLine("/memory search visible")
	first.mustSee("MEMORY RECORDS")
	second.sendLine("/memory search fixed")
	second.mustSee("MEMORY RECORDS")

	if strings.Contains(first.output(), "No memory records") {
		t.Errorf("the Claude window found no memory:\n%s", tail(first.output(), 1200))
	}
	if strings.Contains(second.output(), "No memory records") {
		t.Errorf("the Codex window could not see Claude's work:\n%s", tail(second.output(), 1200))
	}

	// Each provider keeps its own import index, so two windows watching the same
	// project cannot corrupt one another's progress.
	for _, provider := range []string{"claude", "codex"} {
		path := filepath.Join(resolved, ".marshal", provider, "history-index.json")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s has no import index: %v", provider, err)
			continue
		}
		if len(data) == 0 {
			t.Errorf("%s import index is empty", provider)
		}
	}
}
