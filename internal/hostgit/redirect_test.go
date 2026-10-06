package hostgit

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

func TestHostGitPinsWorktree(t *testing.T) {
	repo := testgit.New(t)
	tree := filepath.Join(t.TempDir(), "tree")
	victim := t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
	}
	run(repo.Path(), "worktree", "add", "--detach", tree, "HEAD")
	meta := filepath.Join(t.TempDir(), "private-meta")
	if err := os.CopyFS(meta, os.DirFS(filepath.Join(repo.Path(), ".git"))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, ".git"), []byte("gitdir: "+meta+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(victim, "outside.txt"), []byte("synthetic-host-only-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	run(tree, "config", "core.worktree", victim)
	run(tree, "config", "core.bare", "true")
	outsideIgnore := filepath.Join(victim, "ignore")
	if err := os.WriteFile(outsideIgnore, []byte("worker.txt\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run(tree, "config", "core.excludesFile", outsideIgnore)
	run(tree, "config", "extensions.worktreeConfig", "true")
	run(tree, "config", "--worktree", "core.worktree", victim)
	if err := os.WriteFile(filepath.Join(tree, "worker.txt"), []byte("worker result"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd, err := Command(t.Context(), tree, "add", "-A")
	if err != nil {
		t.Fatal(err)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("add: %v %s", err, out)
	}
	cmd, err = Command(t.Context(), tree, "show", ":worker.txt")
	if err != nil {
		t.Fatal(err)
	}
	if out, err := cmd.Output(); err != nil || string(out) != "worker result" {
		t.Fatalf("worker file not staged: %q %v", out, err)
	}
	cmd, err = Command(t.Context(), tree, "show", ":outside.txt")
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.Output()
	if err == nil {
		t.Fatalf("outside file entered worker index: %s", out)
	}
	cmd, err = Command(t.Context(), tree, "reset", "--hard", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("reset: %v %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(tree, "worker.txt")); !os.IsNotExist(err) {
		t.Fatalf("worker reset did not execute: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(victim, "outside.txt")); err != nil || string(data) != "synthetic-host-only-secret" {
		t.Fatalf("outside file changed: %q %v", data, err)
	}
}

func TestHostGitDoesNotFallBackToParentRepository(t *testing.T) {
	repo := testgit.New(t)
	tree := filepath.Join(repo.Path(), ".marshal", "worktrees", "worker")
	if err := os.MkdirAll(tree, 0700); err != nil {
		t.Fatal(err)
	}
	cmd, err := Command(t.Context(), tree, "add", "-A")
	if err != nil {
		t.Fatal(err)
	}
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("missing worker metadata selected parent: %s", out)
	}
}
