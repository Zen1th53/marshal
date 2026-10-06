package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
)

func TestInitWithoutBaselineExplainsCommands(t *testing.T) {
	for _, repository := range []bool{false, true} {
		t.Run(map[bool]string{false: "directory", true: "unborn"}[repository], func(t *testing.T) {
			dir := t.TempDir()
			if repository {
				testGit(t, dir, "init", "-q")
			}
			before := listDir(t, dir)
			out, stderr, code := runCLI(t, dir, "init")
			if code == 0 {
				t.Fatal("init succeeded without a baseline")
			}
			for _, want := range []string{"git commit --allow-empty --only -m \"Initial commit\"", "marshal init"} {
				if !strings.Contains(stderr, want) {
					t.Errorf("missing %q in %s", want, stderr)
				}
			}
			if !repository && !strings.Contains(stderr, "git init") {
				t.Error("missing git init instruction")
			}
			assertNoInternalLeak(t, out+stderr)
			after := listDir(t, dir)
			if strings.Join(before, "\n") != strings.Join(after, "\n") {
				t.Fatalf("non-interactive init mutated directory: %v -> %v", before, after)
			}
		})
	}
}

func testGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func assertInitializedProject(t *testing.T, dir string) {
	t.Helper()
	runtime, err := app.Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("initialized project cannot open: %v", err)
	}
	runtime.Close()
	if _, err := os.Stat(filepath.Join(dir, ".marshal")); err != nil {
		t.Fatal(err)
	}
	testGit(t, dir, "rev-parse", "--verify", "HEAD")
}

func assertInitFilesRecorded(t *testing.T, dir string) {
	t.Helper()
	for _, name := range []string{"CAPABILITIES.yaml", "PACK-VERSION.yaml", "RUNTIME-VERSION.yaml"} {
		testGit(t, dir, "cat-file", "-e", "HEAD:"+name)
	}
	if got := testGit(t, dir, "ls-files", ".marshal"); got != "" {
		t.Fatalf("private runtime state tracked: %s", got)
	}
	if got := testGit(t, dir, "check-ignore", ".marshal/state.db"); got != ".marshal/state.db" {
		t.Fatalf("runtime state not excluded: %s", got)
	}
}

func TestInitExistingRepoRecordsDefaultsAndKeepsCheckoutClean(t *testing.T) {
	for _, location := range []string{"root", "subdirectory", "linked-worktree"} {
		t.Run(location, func(t *testing.T) {
			dir := t.TempDir()
			testGit(t, dir, "init", "-q")
			testGit(t, dir, "config", "user.name", "Test Operator")
			testGit(t, dir, "config", "user.email", "operator@example.invalid")
			testGit(t, dir, "commit", "--allow-empty", "-qm", "baseline")
			exclude := filepath.Join(dir, ".git", "info", "exclude")
			if err := os.WriteFile(exclude, []byte("# keep this\n/local-only\n/.marshal/\n!/.marshal/\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("/build/\n"), 0600); err != nil {
				t.Fatal(err)
			}
			testGit(t, dir, "add", ".gitignore")
			testGit(t, dir, "commit", "-qm", "project ignores")
			before := testGit(t, dir, "rev-parse", "HEAD")
			initDir := dir
			switch location {
			case "subdirectory":
				initDir = filepath.Join(dir, "src")
				if err := os.Mkdir(initDir, 0755); err != nil {
					t.Fatal(err)
				}
			case "linked-worktree":
				linked := filepath.Join(t.TempDir(), "linked")
				testGit(t, dir, "worktree", "add", "-b", "init-linked", linked, "HEAD")
				dir, initDir = linked, linked
			}
			for i := 0; i < 2; i++ {
				_, stderr, code := runCLI(t, initDir, "init")
				if code != 0 {
					t.Fatalf("init: %d %s", code, stderr)
				}
				assertInitializedProject(t, dir)
				assertInitFilesRecorded(t, dir)
				if got := testGit(t, dir, "status", "--porcelain"); got != "" {
					t.Fatalf("dirty after init: %s", got)
				}
			}
			if testGit(t, dir, "rev-parse", "HEAD^") != before {
				t.Fatal("init did not make exactly one configuration commit")
			}
			data, err := os.ReadFile(exclude)
			if err != nil || !strings.HasPrefix(string(data), "# keep this\n/local-only\n/.marshal/\n!/.marshal/\n") || strings.Count(string(data), "/.marshal/") != 3 {
				t.Fatalf("exclude not preserved/idempotent: %q %v", data, err)
			}
			data, err = os.ReadFile(filepath.Join(dir, ".gitignore"))
			if err != nil || string(data) != "/build/\n" {
				t.Fatalf("gitignore changed: %q %v", data, err)
			}
		})
	}
}
