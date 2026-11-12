package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/tmux"
)

// unsetEnv removes a variable for the test and restores it afterwards.
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "")
	os.Unsetenv(key)
}

// fakeHome makes a home folder that is itself a Git repository with one
// commit, as it is for people who keep their dotfiles in Git. With identity,
// the user's Git identity is configured globally there; without it Git knows
// nobody.
func fakeHome(t *testing.T, identity bool) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	for _, key := range []string{"XDG_CONFIG_HOME", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM", "GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "EMAIL", "GIT_DIR", "GIT_WORK_TREE"} {
		unsetEnv(t, key)
	}
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	testGit(t, home, "init", "-q")
	if identity {
		testGit(t, home, "config", "--global", "user.name", "Test")
		testGit(t, home, "config", "--global", "user.email", "test@example.invalid")
	}
	if err := os.WriteFile(filepath.Join(home, ".profile"), []byte("# dotfile\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testGit(t, home, "add", ".profile")
	testGit(t, home, "-c", "user.name=Home", "-c", "user.email=home@example.invalid", "commit", "-qm", "dotfiles")
	return home
}

// homeState is what must not change in the home repository.
func homeState(t *testing.T, home string) string {
	t.Helper()
	return testGit(t, home, "rev-parse", "HEAD") + "\n" + testGit(t, home, "ls-files", "--stage") + "\n" + testGit(t, home, "branch", "--list")
}

func assertStartedRepository(t *testing.T, dir, stderr string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatalf("no repository was started in %s: %v", dir, err)
	}
	if got := testGit(t, dir, "rev-parse", "--show-toplevel"); got != dir {
		t.Fatalf("the new repository root is %s, want %s", got, dir)
	}
	if got := testGit(t, dir, "log", "--reverse", "--format=%an <%ae> %s"); !strings.HasPrefix(got, "Test <test@example.invalid> Initial commit") {
		t.Fatalf("the first commit does not carry the user's identity: %q", got)
	}
	if strings.Count(stderr, "Started a new Git repository for this project in ") != 1 {
		t.Fatalf("the started line is missing or repeated: %q", stderr)
	}
}

func TestInitStartsRepositoryInHomeRepositorySubfolder(t *testing.T) {
	home := fakeHome(t, true)
	before := homeState(t, home)
	dir := filepath.Join(home, "Desktop", "testM")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := runCLI(t, dir, "init")
	if code != 0 {
		t.Fatalf("init code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "Started a new Git repository for this project in ~/Desktop/testM.") {
		t.Fatalf("missing started line: %q", stderr)
	}
	assertStartedRepository(t, dir, stderr)
	testGit(t, dir, "cat-file", "-e", "HEAD:notes.txt")
	assertInitializedProject(t, dir)
	assertInitFilesRecorded(t, dir)
	if got := testGit(t, dir, "status", "--porcelain"); got != "" {
		t.Fatalf("init left a dirty checkout: %q", got)
	}
	if after := homeState(t, home); after != before {
		t.Fatalf("the home repository changed:\n%s\n---\n%s", before, after)
	}
	if _, err := os.Stat(statepath.Path(home)); !os.IsNotExist(err) {
		t.Fatalf("project state was created in the home folder: %v", err)
	}
	// Running again finds the new repository and changes nothing.
	head := testGit(t, dir, "rev-parse", "HEAD")
	_, stderr, code = runCLI(t, dir, "init")
	if code != 0 || strings.Contains(stderr, "Started a new Git repository") || testGit(t, dir, "rev-parse", "HEAD") != head {
		t.Fatalf("repeated init code=%d stderr=%q", code, stderr)
	}
}

func TestInitStartsRepositoryOutsideAnyRepository(t *testing.T) {
	fakeHome(t, true)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := runCLI(t, dir, "init")
	if code != 0 {
		t.Fatalf("init code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	assertStartedRepository(t, dir, stderr)
	if files := testGit(t, dir, "ls-tree", "--name-only", "HEAD^"); files != "" {
		t.Fatalf("an empty folder's first commit holds files: %s", files)
	}
	assertInitializedProject(t, dir)
}

func TestInitInsideOrdinaryRepositoryIsUnchanged(t *testing.T) {
	fakeHome(t, true)
	root := gitDirectory(t)
	sub := filepath.Join(root, "src")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := runCLI(t, sub, "init")
	if code != 0 || strings.Contains(stderr, "Started a new Git repository") {
		t.Fatalf("init code=%d stderr=%q", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(sub, ".git")); !os.IsNotExist(err) {
		t.Fatalf("a repository was started inside an ordinary repository: %v", err)
	}
	assertInitializedProject(t, root)
}

func TestHomeFolderItselfIsRefusedPlainly(t *testing.T) {
	home := fakeHome(t, true)
	before := homeState(t, home)
	for _, args := range [][]string{{"init"}, nil, {"tui"}, {"setup", "status"}, {"doctor"}} {
		stdout, stderr, code := runCLI(t, home, args...)
		if code == 0 || stdout != "" || strings.TrimSpace(stderr) != statepath.RefusedRootMessage {
			t.Fatalf("%v code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
	if after := homeState(t, home); after != before {
		t.Fatal("the home repository changed")
	}
	if _, err := os.Stat(statepath.Path(home)); !os.IsNotExist(err) {
		t.Fatalf("project state was created in the home folder: %v", err)
	}
}

func TestMissingGitIdentityStopsBeforeCreatingAnything(t *testing.T) {
	home := fakeHome(t, false)
	for _, dir := range []string{filepath.Join(home, "Desktop", "testM"), t.TempDir()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		before := listDir(t, dir)
		_, stderr, code := runCLI(t, dir, "init")
		if code == 0 {
			t.Fatal("init succeeded without a Git identity")
		}
		for _, want := range []string{"Git does not know who you are yet", `git config --global user.name "Your Name"`, `git config --global user.email "you@example.com"`} {
			if !strings.Contains(stderr, want) {
				t.Fatalf("missing %q in %q", want, stderr)
			}
		}
		assertNoInternalLeak(t, stderr)
		if after := listDir(t, dir); strings.Join(after, "\n") != strings.Join(before, "\n") {
			t.Fatalf("init changed the folder: %v -> %v", before, after)
		}
	}
}

func TestSetupStatusInHomeRepositorySubfolderIsNotReady(t *testing.T) {
	home := fakeHome(t, true)
	// A home folder once set up for Marshal must not make a subfolder look ready.
	if err := os.MkdirAll(statepath.Path(home), 0o700); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "Desktop", "testM")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stdout, _, code := runCLI(t, dir, "setup", "status")
	if code != 0 {
		t.Fatalf("setup status exited %d", code)
	}
	if strings.Contains(stdout, "Readiness: READY") || strings.Contains(stdout, "[READY          ] project.repository") || strings.Contains(stdout, "[READY          ] project.initialized") {
		t.Fatalf("setup reported the home repository as ready:\n%s", stdout)
	}
	if !strings.Contains(stdout, "project.repository       This folder is inside the Git repository of your home folder. Run `marshal` here and it will start a separate repository for this project.") {
		t.Fatalf("setup did not say why:\n%s", stdout)
	}
	jsonOut, _, code := runCLI(t, dir, "--json", "setup", "status")
	if code != 0 || !strings.Contains(jsonOut, "STARTUP_PROJECT_ROOT_REFUSED") {
		t.Fatalf("code=%d json=%s", code, jsonOut)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); !os.IsNotExist(err) {
		t.Fatal("setup status started a repository")
	}
}

func TestDoctorWithoutProjectCollapsesUncheckedLines(t *testing.T) {
	home := fakeHome(t, true)
	dir := filepath.Join(home, "Desktop", "testM")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stdout, _, code := runCLI(t, dir, "doctor")
	if code != 2 {
		t.Fatalf("doctor exited %d:\n%s", code, stdout)
	}
	if got := strings.Count(stdout, "[FAIL]"); got != 1 {
		t.Fatalf("doctor printed %d FAIL lines:\n%s", got, stdout)
	}
	for _, want := range []string{
		"repository       This folder is inside the Git repository of your home folder. Run `marshal` here and it will start a separate repository for this project.",
		"Other checks need a project; run marshal in your project folder first.",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "not checked because repository discovery failed") {
		t.Fatalf("unchecked lines were not collapsed:\n%s", stdout)
	}
	// The machine-readable report keeps every result as it was.
	jsonOut, _, _ := runCLI(t, dir, "--json", "doctor")
	var report struct {
		Results []struct {
			Name, Verdict, Detail string
		}
	}
	if err := json.Unmarshal([]byte(jsonOut), &report); err != nil {
		t.Fatalf("%v: %s", err, jsonOut)
	}
	unchecked := 0
	for _, result := range report.Results {
		if result.Name == "repository" && result.Detail != "not a usable Git repository" {
			t.Fatalf("repository detail changed in JSON: %q", result.Detail)
		}
		if result.Detail == "not checked because repository discovery failed" {
			unchecked++
		}
	}
	if unchecked != 16 {
		t.Fatalf("JSON report has %d unchecked results, want 16", unchecked)
	}
}

// The control centre prepares the folder the same way and sets Marshal up in
// it before going on to tmux.
func TestControlCenterStartsRepositoryInHomeRepositorySubfolder(t *testing.T) {
	home := fakeHome(t, true)
	before := homeState(t, home)
	dir := filepath.Join(home, "Desktop", "testM")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX", "")
	t.Setenv("MARSHAL_TEST_FORCE_TMUX_LAUNCH", "1")
	fake := filepath.Join(t.TempDir(), "tmux")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(fake)
	defer tmux.ResetBinaryPath()
	_, stderr, _ := runCLI(t, dir)
	assertStartedRepository(t, dir, stderr)
	assertInitializedProject(t, dir)
	if after := homeState(t, home); after != before {
		t.Fatal("the home repository changed")
	}
	if strings.Contains(stderr, "isn't set up") || strings.Contains(stderr, "home folder") {
		t.Fatalf("the control centre did not continue as for a normal project: %q", stderr)
	}
}
