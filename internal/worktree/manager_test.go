package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

func TestPrepareCreatesTaskBranchAtExactBase(t *testing.T) {
	repo := testgit.New(t)
	root := filepath.Join(repo.Path(), ".marshal", "worktrees")
	manager := New(repo.Path(), root)
	request := model.WorktreeRequest{
		TaskID: "TASK-001", Branch: "agent/task-001-local", BaseCommit: repo.HEAD(t),
	}
	got, err := manager.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if got.TaskID != request.TaskID || got.Branch != request.Branch || got.HEAD != request.BaseCommit {
		t.Fatalf("worktree = %#v", got)
	}
	if got.Path != filepath.Join(root, "TASK-001") {
		t.Fatalf("path = %q", got.Path)
	}
	again, err := manager.Prepare(context.Background(), request)
	if err != nil {
		t.Fatalf("idempotent Prepare: %v", err)
	}
	if again != got {
		t.Fatalf("second worktree = %#v, want %#v", again, got)
	}
}

func TestPrepareRejectsBranchCollision(t *testing.T) {
	repo := testgit.New(t)
	manager := New(repo.Path(), filepath.Join(repo.Path(), ".marshal", "worktrees"))
	base := repo.HEAD(t)
	if _, err := manager.Prepare(context.Background(), model.WorktreeRequest{
		TaskID: "TASK-001", Branch: "agent/shared", BaseCommit: base,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Prepare(context.Background(), model.WorktreeRequest{
		TaskID: "TASK-002", Branch: "agent/shared", BaseCommit: base,
	}); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("collision error = %v", err)
	}
}

func TestDirtyTaskWorktreeIsNeverDestroyed(t *testing.T) {
	repo := testgit.New(t)
	manager := New(repo.Path(), filepath.Join(repo.Path(), ".marshal", "worktrees"))
	worktree, err := manager.Prepare(context.Background(), model.WorktreeRequest{
		TaskID: "TASK-001", Branch: "agent/task-001", BaseCommit: repo.HEAD(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	userFile := filepath.Join(worktree.Path, "user.txt")
	if err := os.WriteFile(userFile, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Remove(context.Background(), worktree); !errors.Is(err, model.ErrDirtyWorktree) {
		t.Fatalf("remove error = %v", err)
	}
	if data, err := os.ReadFile(userFile); err != nil || string(data) != "keep" {
		t.Fatalf("dirty data changed: data=%q err=%v", data, err)
	}
	if state, err := manager.Inspect(context.Background(), worktree.Path); err != nil || !state.Dirty {
		t.Fatalf("inspect dirty state = %#v, err=%v", state, err)
	}
}

func TestPreparePreservesUnexpectedTargetDirectory(t *testing.T) {
	repo := testgit.New(t)
	root := filepath.Join(repo.Path(), ".marshal", "worktrees")
	target := filepath.Join(root, "TASK-001")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(target, "unknown")
	if err := os.WriteFile(marker, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := New(repo.Path(), root)
	_, err := manager.Prepare(context.Background(), model.WorktreeRequest{
		TaskID: "TASK-001", Branch: "agent/task-001", BaseCommit: repo.HEAD(t),
	})
	if !errors.Is(err, model.ErrConflict) {
		t.Fatalf("error = %v", err)
	}
	if data, readErr := os.ReadFile(marker); readErr != nil || string(data) != "preserve" {
		t.Fatalf("unexpected target changed: data=%q err=%v", data, readErr)
	}
}
