package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/tmux"
)

// consentLaunch opens Marshal with no arguments in dir, answering the
// consent screen with input; interactive says whether a terminal is there.
func consentLaunch(t *testing.T, dir, input string, interactive bool) (stderr string, code int) {
	t.Helper()
	t.Setenv("TMUX", "")
	t.Setenv("MARSHAL_TEST_FORCE_TMUX_LAUNCH", "1")
	// tmux fails, so a launch that gets past setup stops right after it.
	fake := filepath.Join(t.TempDir(), "tmux")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(fake)
	t.Cleanup(tmux.ResetBinaryPath)
	previous := setupConsentInteractive
	setupConsentInteractive = func(*command) bool { return interactive }
	t.Cleanup(func() { setupConsentInteractive = previous })
	var out, errBuf bytes.Buffer
	code = Execute(context.Background(), dir, nil, strings.NewReader(input), &out, &errBuf)
	return errBuf.String(), code
}

// dirtyGitDirectory is gitDirectory with one modified and one untracked file.
func dirtyGitDirectory(t *testing.T) string {
	t.Helper()
	dir := gitDirectory(t)
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func headSubject(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "log", "-1", "--format=%s").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func assertNotSetUp(t *testing.T, dir string) {
	t.Helper()
	if got := headSubject(t, dir); got != "chore: init" {
		t.Fatalf("a commit was made without consent: %q", got)
	}
	for _, path := range []string{statepath.Path(dir, "state.db"), filepath.Join(dir, "CAPABILITIES.yaml")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("setup wrote %s without consent: %v", path, err)
		}
	}
	exclude, _ := os.ReadFile(filepath.Join(dir, ".git", "info", "exclude"))
	if strings.Contains(string(exclude), "/.marshal/") {
		t.Fatalf("exclude changed without consent: %q", exclude)
	}
}

func TestSetupConsentDeclineMakesNoCommit(t *testing.T) {
	t.Setenv(acceptSetupEnv, "")
	dir := dirtyGitDirectory(t)
	stderr, code := consentLaunch(t, dir, "n\n", true)
	if code != 0 {
		t.Fatalf("decline should exit cleanly: code=%d stderr=%q", code, stderr)
	}
	for _, want := range []string{
		"commit CAPABILITIES.yaml, PACK-VERSION.yaml and RUNTIME-VERSION.yaml on branch ",
		"add .marshal/ to .git/info/exclude",
		"2 uncommitted files are not included",
		"Set up Marshal here? [y/N]",
		setupDeclinedLine,
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("missing %q in %q", want, stderr)
		}
	}
	if strings.Contains(stderr, "Set up Marshal in this project.") {
		t.Fatalf("declined launch reported setup: %q", stderr)
	}
	assertNotSetUp(t, dir)
}

func TestSetupConsentClosedInputIsDecline(t *testing.T) {
	t.Setenv(acceptSetupEnv, "")
	dir := gitDirectory(t)
	if stderr, code := consentLaunch(t, dir, "", true); code != 0 || !strings.Contains(stderr, setupDeclinedLine) {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	assertNotSetUp(t, dir)
}

func TestSetupConsentNonInteractiveRefusesWithoutAccept(t *testing.T) {
	t.Setenv(acceptSetupEnv, "")
	dir := gitDirectory(t)
	stderr, code := consentLaunch(t, dir, "y\n", false)
	if code == 0 || !strings.Contains(stderr, acceptSetupEnv+"=1") || !strings.Contains(stderr, "marshal init") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	assertNotSetUp(t, dir)
}

// Accepting gives today's result: the same one commit of the defaults, on
// the checked-out branch, and the setup line.
func TestSetupConsentAcceptCommitsAsBefore(t *testing.T) {
	t.Setenv(acceptSetupEnv, "")
	dir := dirtyGitDirectory(t)
	stderr, _ := consentLaunch(t, dir, "y\n", true)
	if !strings.Contains(stderr, "2 uncommitted files are not included") {
		t.Fatalf("dirty count not shown: %q", stderr)
	}
	want := `Set up Marshal in this project. Committed CAPABILITIES.yaml, PACK-VERSION.yaml and RUNTIME-VERSION.yaml to your repository (commit "Initialize MARSHAL project").`
	if !strings.Contains(stderr, want) {
		t.Fatalf("no setup line in %q", stderr)
	}
	if got := headSubject(t, dir); got != initCommitSubject {
		t.Fatalf("HEAD = %q", got)
	}
	if _, err := os.Stat(statepath.Path(dir, "state.db")); err != nil {
		t.Fatalf("project was not set up: %v", err)
	}
	// The user's uncommitted work is left as it was.
	out, _ := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	if !strings.Contains(string(out), " M README.md") || !strings.Contains(string(out), "?? notes.txt") {
		t.Fatalf("uncommitted work changed: %q", out)
	}
	// Opening again neither asks nor sets up a second time.
	stderr, _ = consentLaunch(t, dir, "", false)
	if strings.Contains(stderr, "Set up Marshal") || strings.Contains(stderr, acceptSetupEnv) {
		t.Fatalf("second open asked again: %q", stderr)
	}
}

func TestCountUncommitted(t *testing.T) {
	status := " M a.go\x00?? b.txt\x00R  new.go\x00old.go\x00?? .marshal/\x00?? CAPABILITIES.yaml\x00"
	if got := countUncommitted(status, []string{"CAPABILITIES.yaml"}); got != 3 {
		t.Fatalf("got %d, want 3", got)
	}
	if got := countUncommitted("", nil); got != 0 {
		t.Fatalf("clean tree counted %d", got)
	}
}

func TestSetupPreviewRender(t *testing.T) {
	p := setupPreview{Files: []string{"CAPABILITIES.yaml"}, Commit: "abc1234", Uncommitted: 1, HostWorkers: true, Telemetry: true}
	got := p.Render()
	for _, want := range []string{
		"commit CAPABILITIES.yaml on a detached HEAD",
		"Workers start from your last commit (abc1234). 1 uncommitted file is not included",
		"Workers run as you, with your file and network access and no sandbox.",
		"Anonymous telemetry is on",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	p = setupPreview{Branch: "main", Commit: "abc1234"}
	got = p.Render()
	if !strings.Contains(got, "make no commit (its files are already in branch main)") || strings.Contains(got, "no sandbox") || !strings.Contains(got, "telemetry is off") {
		t.Fatalf("got %q", got)
	}
}
