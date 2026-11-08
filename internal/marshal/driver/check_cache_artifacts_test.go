package driver

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// writingRunner stands in for the check sandbox: it leaves the given files
// behind in the check checkout (removing a file when its content is "-").
func writingRunner(t *testing.T, files map[string]string) context.Context {
	t.Helper()
	return WithCheckRunner(t.Context(), func(_ context.Context, dir, command string) marshal.CommandRecord {
		for name, content := range files {
			path := filepath.Join(dir, name)
			if content == "-" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				continue
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return marshal.CommandRecord{Command: command}
	})
}

func ignoringRepo(t *testing.T) (string, string) {
	t.Helper()
	_, wt := newTask(t)
	if err := os.WriteFile(filepath.Join(wt, ".gitignore"), []byte("build/\n*.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, wt, "git", "add", ".gitignore")
	run(t, wt, "git", "commit", "-q", "-m", "ignore build output")
	return wt, run(t, wt, "git", "rev-parse", "HEAD")
}

// A check that only leaves ignored build output or interpreter caches behind
// (`python3 -m unittest` writes tools/tests/__pycache__) has not changed the
// tested source. Run RUN-1793898762273028817 refused two passing unittest
// checks with "check changed tested source or its identity" for this.
func TestCheckIgnoredAndCacheArtifactsAreNotSourceChanges(t *testing.T) {
	wt, head := ignoringRepo(t)
	for _, files := range []map[string]string{
		{"build/out.o": "x", "check.log": "y"},
		{"tools/tests/__pycache__/test_x.cpython-313.pyc": "c"},
		{".pytest_cache/v/cache/lastfailed": "{}", ".mypy_cache/a": "", ".ruff_cache/b": ""},
	} {
		if rec := runCheck(writingRunner(t, files), wt, head, "check", time.Minute); rec.ExitCode != 0 {
			t.Fatalf("%v: check refused (%d): %s", files, rec.ExitCode, rec.Output)
		}
	}
}

// Every tracked change and every untracked file that is neither ignored nor a
// cache still refuses the check.
func TestCheckSourceChangesStillRefused(t *testing.T) {
	wt, head := ignoringRepo(t)
	for _, files := range []map[string]string{
		{"README": "modified"},
		{".gitignore": "-"},
		{"generated.py": "new"},
		{"tools/tests/__pycache__/x.pyc": "c", "tools/tests/new_test.py": "new"},
		{"cachelike/__pycache__.py": "x"},
	} {
		if rec := runCheck(writingRunner(t, files), wt, head, "check", time.Minute); rec.ExitCode == 0 {
			t.Fatalf("%v: a source change passed the check guard", files)
		}
	}
	// The native runner keeps the same guard.
	if rec := runCheck(t.Context(), wt, head, "printf modified > README", time.Minute); rec.ExitCode == 0 {
		t.Fatal("source-modifying check passed")
	}
}
