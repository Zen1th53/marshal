package execution

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceTreeDigestBindsDirtyFilesAndDoesNotFollowSymlinks(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a"), []byte("one"), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := WorkspaceTreeDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a"), []byte("two"), 0600); err != nil {
		t.Fatal(err)
	}
	second, _ := WorkspaceTreeDigest(root)
	if first == second {
		t.Fatal("content mutation did not change tree")
	}
	outside := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(outside, []byte("secret-one"), 0600)
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	linked, _ := WorkspaceTreeDigest(root)
	os.WriteFile(outside, []byte("secret-two"), 0600)
	after, _ := WorkspaceTreeDigest(root)
	if linked != after {
		t.Fatal("digest followed symlink outside workspace")
	}
}

func TestWorkspaceTreeDigestSkipsNestedRepositoriesAndWorktrees(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a"), []byte("one"), 0600); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, ".claude", "worktrees", "agent")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	// A linked worktree carries a .git file; a nested clone a .git directory.
	if err := os.WriteFile(filepath.Join(nested, ".git"), []byte("gitdir: elsewhere\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := WorkspaceTreeDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "busy"), []byte("changing"), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := WorkspaceTreeDigest(root)
	if err != nil || before != after {
		t.Fatalf("nested worktree changed the workspace digest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claude", "own"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if own, _ := WorkspaceTreeDigest(root); own == after {
		t.Fatal("a workspace file beside the nested worktree was ignored")
	}
}

func TestWorkspaceTreeDigestSkipsInterpreterCaches(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "calc.py"), []byte("def add(a, b):\n    return a + b\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0700); err != nil {
		t.Fatal(err)
	}
	before, err := WorkspaceTreeDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"__pycache__", "pkg/__pycache__", ".pytest_cache"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, dir, "cache.pyc"), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	after, err := WorkspaceTreeDigest(root)
	if err != nil || before == "" || after != before {
		t.Fatalf("a test run's caches changed the workspace digest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "real.py"), []byte("x = 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if real, _ := WorkspaceTreeDigest(root); real == after {
		t.Fatal("a source file beside a cache was ignored")
	}
}
