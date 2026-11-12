package cli

import (
	"bytes"
	"context"
	"fmt"
	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/tmux"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests exercise the Process 01 entry point through the real CLI, in
// real directories, because the defect being fixed was a behaviour of the
// binary rather than of a function.

func runCLI(t *testing.T, root string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errBuf bytes.Buffer
	code = Execute(context.Background(), root, args, strings.NewReader(""), &out, &errBuf)
	return out.String(), errBuf.String(), code
}

// plainDirectory is a directory with no repository and no project.
func plainDirectory(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// gitDirectory is a repository with a commit but no MARSHAL project.
func gitDirectory(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(name string, args ...string) {
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable in this environment: %v: %s", err, out)
		}
	}
	run("git", "init", "-q", ".")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("git", "add", "-A")
	run("git", "commit", "-qm", "chore: init")
	return dir
}

// Startup failures must reach the terminal before tmux is launched.
func TestTUIRejectsUninitializedProjectWithoutWrites(t *testing.T) {
	for _, partial := range []bool{false, true} {
		for _, args := range [][]string{nil, {"tui"}} {
			t.Run(fmt.Sprintf("partial=%t/args=%v", partial, args), func(t *testing.T) {
				dir := gitDirectory(t)
				if partial {
					for _, name := range []string{"claude_session", "codex_appserver"} {
						if err := os.MkdirAll(statepath.Path(dir, name), 0700); err != nil {
							t.Fatal(err)
						}
					}
				}
				t.Setenv("TMUX", "")
				t.Setenv("MARSHAL_TEST_FORCE_TMUX_LAUNCH", "1")
				// Any tmux operation would fail and leave a marker.
				marker := filepath.Join(t.TempDir(), "called")
				fake := filepath.Join(t.TempDir(), "tmux")
				if err := os.WriteFile(fake, []byte(fmt.Sprintf("#!/bin/sh\ntouch %q\nexit 1\n", marker)), 0700); err != nil {
					t.Fatal(err)
				}
				tmux.SetBinaryPath(fake)
				defer tmux.ResetBinaryPath()
				stdout, stderr, code := runCLI(t, dir, args...)
				if code == 0 || stdout != "" {
					t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
				}
				for _, want := range []string{"Marshal isn't set up in this project yet.", "Run `marshal init` here."} {
					if !strings.Contains(stderr, want) {
						t.Fatalf("missing %q in %q", want, stderr)
					}
				}
				for _, path := range []string{marker, statepath.Path(dir, "state.db"), statepath.Path(dir, "runtime-owner.lock"), statepath.Path(dir, "logs")} {
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Fatalf("startup wrote %s: %v", path, err)
					}
				}
			})
		}
	}
}

// Outside any repository the control centre would start one, but not without
// the user's Git identity; then it stops, says how to set it and creates
// nothing.
func TestTUIOutsideRepositoryReportsFailure(t *testing.T) {
	for _, args := range [][]string{nil, {"tui"}} {
		dir := plainDirectory(t)
		fakeHome(t, false)
		stdout, stderr, code := runCLI(t, dir, args...)
		if code == 0 || stdout != "" {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); !os.IsNotExist(err) {
			t.Fatalf("a repository was started without an identity: %v", err)
		}
		for _, want := range []string{"Git does not know who you are yet", "git config --global user.name"} {
			if !strings.Contains(stderr, want) {
				t.Fatalf("missing %q in %q", want, stderr)
			}
		}
		assertNoInternalLeak(t, stderr)
		if _, err := os.Stat(statepath.Path(dir)); !os.IsNotExist(err) {
			t.Fatalf("home startup created state: %v", err)
		}
	}
}

// Setup assesses and never mutates: running it in a plain directory must not
// create a repository, a project directory or a database.
func TestSetupNeverMutatesTheDirectory(t *testing.T) {
	dir := plainDirectory(t)
	before := listDir(t, dir)

	stdout, _, code := runCLI(t, dir, "setup")
	if code != 0 {
		t.Fatalf("setup exited %d", code)
	}
	if !strings.Contains(stdout, "Readiness:") {
		t.Fatalf("setup did not report readiness:\n%s", stdout)
	}

	after := listDir(t, dir)
	if len(before) != len(after) {
		t.Fatalf("setup changed the directory: before=%v after=%v", before, after)
	}
	for _, forbidden := range []string{".git", statepath.DirName, "CAPABILITIES.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, forbidden)); err == nil {
			t.Fatalf("setup created %s without being asked", forbidden)
		}
	}
}

// Entering the control center must likewise create nothing.
func TestControlCenterEntryNeverMutates(t *testing.T) {
	// Without a Git identity the folder cannot be prepared, so nothing is made.
	fakeHome(t, false)
	dir := plainDirectory(t)
	if _, _, code := runCLI(t, dir); code == 0 {
		t.Fatalf("control center exited %d", code)
	}
	for _, forbidden := range []string{".git", statepath.DirName, "CAPABILITIES.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, forbidden)); err == nil {
			t.Fatalf("entering the control center created %s", forbidden)
		}
	}
}

// Doctor must remain available where it is needed most.
func TestDoctorRunsWithoutAProject(t *testing.T) {
	stdout, _, code := runCLI(t, plainDirectory(t), "doctor")
	// Doctor reports a non-zero verdict for an unusable directory, which is
	// correct; what matters is that it ran and produced a report.
	if code != 0 && code != 2 {
		t.Fatalf("doctor exited %d", code)
	}
	if !strings.Contains(strings.ToLower(stdout), "doctor") {
		t.Fatalf("doctor produced no report:\n%s", stdout)
	}
}

// The JSON form carries the same assessment, so a machine caller cannot get a
// different answer than a human.
func TestSetupJSONMatchesHumanAssessment(t *testing.T) {
	dir := plainDirectory(t)
	jsonOut, _, code := runCLI(t, dir, "--json", "setup")
	if code != 0 {
		t.Fatalf("setup --json exited %d", code)
	}
	for _, expected := range []string{`"phase"`, `"checks"`, `"capabilities"`} {
		if !strings.Contains(jsonOut, expected) {
			t.Fatalf("the JSON assessment omits %s:\n%s", expected, jsonOut)
		}
	}
	if !strings.Contains(jsonOut, "EXECUTION_BLOCKED") {
		t.Fatalf("the JSON assessment did not report the blocked phase:\n%s", jsonOut)
	}
}

func listDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func assertNoInternalLeak(t *testing.T, output string) {
	t.Helper()
	lowered := strings.ToLower(output)
	for _, leak := range []string{
		"exit status", "goroutine", "panic:", "sqlite", "pragma",
		"rev-parse", "unable to open database",
	} {
		if strings.Contains(lowered, leak) {
			t.Fatalf("internal detail %q reached the user:\n%s", leak, output)
		}
	}
}
