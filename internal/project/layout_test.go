package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/slaves/internal/testutil/testgit"
)

func TestDiscoverRejectsDirectoryOutsideGit(t *testing.T) {
	if _, err := Discover(t.TempDir()); err == nil {
		t.Fatal("Discover succeeded outside a Git repository")
	}
}

func TestDiscoverReturnsCanonicalRepositoryIdentity(t *testing.T) {
	repo := testgit.New(t)
	nested := filepath.Join(repo.Path(), "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}

	layout, err := Discover(nested)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if layout.Root != testgit.Canonical(t, repo.Path()) {
		t.Fatalf("root = %q", layout.Root)
	}
	if layout.Branch != "main" {
		t.Fatalf("branch = %q, want main", layout.Branch)
	}
	if layout.HEAD != repo.HEAD(t) {
		t.Fatalf("HEAD = %q, want %q", layout.HEAD, repo.HEAD(t))
	}
	if layout.Database != filepath.Join(layout.Root, ".slaves", "state.db") {
		t.Fatalf("database = %q", layout.Database)
	}
}

func TestEnsureIsIdempotentAndPrivate(t *testing.T) {
	repo := testgit.New(t)
	layout, err := Discover(repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := layout.Ensure(); err != nil {
			t.Fatalf("Ensure %d: %v", i+1, err)
		}
	}
	for _, path := range []string{layout.RuntimeDir, layout.Artifacts, layout.Worktrees, layout.Logs} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Fatalf("%s mode = %o, want 700", path, got)
		}
	}
}
