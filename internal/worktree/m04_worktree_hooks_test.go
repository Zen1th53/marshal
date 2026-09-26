package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

// Creating or reattaching a task worktree must not run a post-checkout hook,
// which a worker could have configured through core.hooksPath.
func TestM04WorktreeOperationsDoNotRunHooks(t *testing.T) {
	repo := testgit.New(t)
	hooks := t.TempDir()
	marker := filepath.Join(t.TempDir(), "hook-ran")
	if err := os.WriteFile(filepath.Join(hooks, "post-checkout"), []byte("#!/bin/sh\necho post-checkout >> '"+marker+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo.Path(), "config", "core.hooksPath", hooks).CombinedOutput(); err != nil {
		t.Fatalf("configure hooks: %s: %v", out, err)
	}
	manager := New(repo.Path(), filepath.Join(repo.Path(), ".marshal", "worktrees"))
	request := model.WorktreeRequest{TaskID: "TASK-001", Branch: "marshal/run/task", BaseCommit: repo.HEAD(t)}
	wt, err := manager.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo.Path(), "-c", "core.hooksPath=/dev/null", "worktree", "remove", wt.Path).CombinedOutput(); err != nil {
		t.Fatalf("remove worktree: %s: %v", out, err)
	}
	if _, err := manager.Resume(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if ran, err := os.ReadFile(marker); !os.IsNotExist(err) {
		t.Fatalf("hooks ran during worktree operations: %q", ran)
	}
}
