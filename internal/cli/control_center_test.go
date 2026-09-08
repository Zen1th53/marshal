package cli

import (
	"bytes"
	"context"
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

// The baseline defect: running marshal outside a repository produced a usage
// screen that neither reported a failure nor offered a way forward.
func TestNoArgsOutsideRepositoryOpensControlCenter(t *testing.T) {
	stdout, stderr, code := runCLI(t, plainDirectory(t))

	if code != 0 {
		t.Fatalf("exit code %d in a plain directory: %s", code, stderr)
	}
	if strings.HasPrefix(stdout, "Usage:") {
		t.Fatal("a usage screen was shown instead of the control center")
	}
	for _, expected := range []string{"doctor", "setup", "help"} {
		if !strings.Contains(stdout, expected) {
			t.Fatalf("the control center omits %q:\n%s", expected, stdout)
		}
	}
	// The user must be told what is wrong and what to do.
	if !strings.Contains(stdout, "not part of a Git repository") {
		t.Fatalf("the situation was not explained:\n%s", stdout)
	}
	assertNoInternalLeak(t, stdout+stderr)
}

// The baseline defect: marshal tui printed raw Git subprocess stderr.
func TestTuiOutsideRepositoryExplainsInsteadOfErroring(t *testing.T) {
	stdout, stderr, code := runCLI(t, plainDirectory(t), "tui")

	if code != 0 {
		t.Fatalf("exit code %d: %s", code, stderr)
	}
	if strings.Contains(stderr, "rev-parse") || strings.Contains(stdout, "rev-parse") {
		t.Fatalf("a Git command leaked to the user:\nstdout=%s\nstderr=%s", stdout, stderr)
	}
	if !strings.Contains(stdout, "doctor") {
		t.Fatalf("Doctor was not offered:\n%s", stdout)
	}
	assertNoInternalLeak(t, stdout+stderr)
}

// The baseline defect: a real repository with no MARSHAL project produced a
// SQLite driver string.
func TestUninitializedProjectIsExplainedNotErrored(t *testing.T) {
	stdout, stderr, code := runCLI(t, gitDirectory(t), "tui")

	if code != 0 {
		t.Fatalf("exit code %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "not been set up") {
		t.Fatalf("the first-run state was not explained:\n%s", stdout)
	}
	if !strings.Contains(stdout, "setup") {
		t.Fatalf("Setup was not offered on a first run:\n%s", stdout)
	}
	assertNoInternalLeak(t, stdout+stderr)
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
	for _, forbidden := range []string{".git", ".marshal", "CAPABILITIES.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, forbidden)); err == nil {
			t.Fatalf("setup created %s without being asked", forbidden)
		}
	}
}

// Entering the control center must likewise create nothing.
func TestControlCenterEntryNeverMutates(t *testing.T) {
	dir := plainDirectory(t)
	if _, _, code := runCLI(t, dir); code != 0 {
		t.Fatalf("control center exited %d", code)
	}
	for _, forbidden := range []string{".git", ".marshal", "CAPABILITIES.yaml"} {
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
