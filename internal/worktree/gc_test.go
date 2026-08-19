package worktree

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

func TestWorktreeGCLifecycle(t *testing.T) {
	repo := testgit.New(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	wm := New(repo.Path(), root)
	ctx := context.Background()

	// 1. Prepare 3 worktrees
	wt1, err := wm.Prepare(ctx, model.WorktreeRequest{
		TaskID:     "TASK-GC-ACTIVE",
		Branch:     "branch-gc-active",
		BaseCommit: repo.HEAD(t),
	})
	if err != nil {
		t.Fatal(err)
	}

	wt2, err := wm.Prepare(ctx, model.WorktreeRequest{
		TaskID:     "TASK-GC-MERGED",
		Branch:     "branch-gc-merged",
		BaseCommit: repo.HEAD(t),
	})
	if err != nil {
		t.Fatal(err)
	}

	wt3, err := wm.Prepare(ctx, model.WorktreeRequest{
		TaskID:     "TASK-GC-DIRTY",
		Branch:     "branch-gc-dirty",
		BaseCommit: repo.HEAD(t),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Make wt3 dirty by writing an untracked file
	dirtyFile := filepath.Join(wt3.Path, "dirty_change.txt")
	if err := os.WriteFile(dirtyFile, []byte("uncommitted content"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 2. Run GC in dry-run mode
	dryRes, err := wm.GC(ctx, GCRequest{
		DryRun:       true,
		ActiveLeases: []string{"TASK-GC-ACTIVE"},
		TaskStatuses: map[string]model.TaskStatus{
			"TASK-GC-ACTIVE": model.TaskWorking,
			"TASK-GC-MERGED": model.TaskMerged,
			"TASK-GC-DIRTY":  model.TaskMerged, // Even if merged, dirty worktree must NOT be deleted
		},
	})
	if err != nil {
		t.Fatalf("dry-run GC: %v", err)
	}

	if len(dryRes.CleanedPaths) != 1 || dryRes.CleanedPaths[0] != wt2.Path {
		t.Fatalf("expected TASK-GC-MERGED to be candidate for cleanup in dry-run, got: %+v", dryRes.CleanedPaths)
	}
	if len(dryRes.SkippedDirty) != 1 || dryRes.SkippedDirty[0] != wt3.Path {
		t.Fatalf("expected TASK-GC-DIRTY to be skipped as dirty, got: %+v", dryRes.SkippedDirty)
	}

	// Verify all worktree paths still exist on disk after dry run
	for _, path := range []string{wt1.Path, wt2.Path, wt3.Path} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("path %s was deleted during dry-run: %v", path, err)
		}
	}

	// 3. Run Real GC
	realRes, err := wm.GC(ctx, GCRequest{
		DryRun:       false,
		TTL:          24 * time.Hour,
		ActiveLeases: []string{"TASK-GC-ACTIVE"},
		TaskStatuses: map[string]model.TaskStatus{
			"TASK-GC-ACTIVE": model.TaskWorking,
			"TASK-GC-MERGED": model.TaskMerged,
			"TASK-GC-DIRTY":  model.TaskMerged,
		},
	})
	if err != nil {
		t.Fatalf("real GC: %v", err)
	}

	// Merged clean worktree (wt2) must be removed
	if _, err := os.Stat(wt2.Path); !os.IsNotExist(err) {
		t.Fatalf("expected wt2 path to be deleted, but still exists: %s", wt2.Path)
	}

	// Active worktree (wt1) must be preserved
	if _, err := os.Stat(wt1.Path); err != nil {
		t.Fatalf("expected active wt1 path to remain untouched, but error: %v", err)
	}

	// Dirty worktree (wt3) must be preserved
	if _, err := os.Stat(wt3.Path); err != nil {
		t.Fatalf("expected dirty wt3 path to remain untouched, but error: %v", err)
	}

	_ = realRes
}
