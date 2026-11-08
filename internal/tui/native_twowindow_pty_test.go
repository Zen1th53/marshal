//go:build linux

package tui

import (
	"github.com/Zen1th53/marshal/internal/statepath"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// twoWindowPatience is this test's wait for a step that crosses both
// windows (a watcher pass, the runtime owner answering). Two TUIs and two
// native doubles share the host here, and a loaded run once took 8.9 s for
// a step the shared 8 s wait allowed; this is more than twice that.
const twoWindowPatience = 20 * time.Second

// mustSeeWithin is mustSee with this test's patience.
func mustSeeWithin(t *testing.T, s *ptySession, want string, patience time.Duration) {
	t.Helper()
	if !s.waitFor(want, patience) {
		t.Fatalf("expected %q on the terminal after %s.\n--- output tail ---\n%s", want, ptyWait(patience), tail(s.output(), 3000))
	}
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

	claudeDir := filepath.Join(claudeHome, "projects", regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(resolved, "-"))
	os.MkdirAll(claudeDir, 0700)
	double("claude", "CLAUDE", "MARSHAL_TEST_CLAUDE_HISTORY", filepath.Join(claudeDir, "session.jsonl"))
	// Neither window passes --session, so both take the default identity.
	first := startInProject(t, project, 40, 160)
	second := startInProject(t, project, 40, 160)
	first.mustSee("Marshal")
	second.mustSee("Marshal")

	grantPTYRead(t, first, claudeDir)
	grantPTYRead(t, first, filepath.Join(codexHome, "sessions"))
	grantPTYRead(t, second, claudeDir)
	grantPTYRead(t, second, filepath.Join(codexHome, "sessions"))
	first.sendLine("/claude")
	first.mustSee("CLAUDE-READY")
	second.sendLine("/codex")
	second.mustSee("CODEX-READY")

	// Both agents are live in the same repository at once.
	first.sendLine("alpha")
	first.mustSee("CLAUDE-IN:<alpha>")
	second.sendLine("beta")
	second.mustSee("CODEX-IN:<beta>")

	// Wait until both watchers have imported what the doubles wrote: each
	// session publishes its import count in live-status.json on every pass.
	// The wait is on that event, not on a fixed sleep a loaded host can
	// outrun.
	imported := func(provider string) bool {
		st, err := readLiveStatus(resolved, provider)
		return err == nil && st.Imported > 0
	}
	deadline := time.Now().Add(ptyWait(twoWindowPatience))
	for !(imported("claude") && imported("codex")) {
		if time.Now().After(deadline) {
			t.Fatalf("the watchers imported nothing within %s (claude %v, codex %v)", ptyWait(twoWindowPatience), imported("claude"), imported("codex"))
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Leave both agents and ask one window what the project remembers. It must
	// carry both agents' work, not only the one this window opened.
	first.send("\r")
	second.send("\r")
	mustSeeWithin(t, first, "Claude exited.", twoWindowPatience)
	mustSeeWithin(t, second, "Codex exited.", twoWindowPatience)

	// Whichever window owns the runtime performs lifecycle decisions. The
	// other window remains a reader; ownership may move during native sessions.
	beforeReview := len(first.output())
	first.sendLine("/memory review")
	var reviewer *ptySession
	deadline = time.Now().Add(ptyWait(twoWindowPatience))
	for time.Now().Before(deadline) {
		response := first.output()[beforeReview:]
		if strings.Contains(response, "MEM-IMPORT-") {
			reviewer = first
			break
		}
		if strings.Contains(response, "another MARSHAL window owns") {
			reviewer = second
			second.sendLine("/memory review")
			break
		}
		time.Sleep(80 * time.Millisecond)
	}
	if reviewer == nil {
		t.Fatalf("neither window returned the memory review or an owner refusal: %s", tail(first.output(), 1200))
	}
	if !reviewer.waitFor("MEM-IMPORT-", twoWindowPatience) {
		t.Fatalf("no memory review listed within %s: %s", ptyWait(twoWindowPatience), tail(reviewer.output(), 1200))
	}
	approvePTYMemoryListed(t, reviewer)
	// Either terminal reads the records approved by the operator in the first.
	first.sendLine("/memory search visible")
	mustSeeWithin(t, first, "MEMORY RECORDS", twoWindowPatience)
	second.sendLine("/memory search fixed")
	mustSeeWithin(t, second, "MEMORY RECORDS", twoWindowPatience)

	if strings.Contains(first.output(), "No memory records") {
		t.Errorf("the Claude window found no memory:\n%s", tail(first.output(), 1200))
	}
	if strings.Contains(second.output(), "No memory records") {
		t.Errorf("the Codex window could not see Claude's work:\n%s", tail(second.output(), 1200))
	}

	// Each provider keeps its own import index, so two windows watching the same
	// project cannot corrupt one another's progress.
	for _, provider := range []string{"claude", "codex"} {
		path := filepath.Join(resolved, statepath.DirName, provider, "history-index.json")
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
