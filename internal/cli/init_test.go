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
