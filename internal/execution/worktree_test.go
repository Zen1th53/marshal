package execution

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWorktreeManager_ValidateTargetPaths(t *testing.T) {
	tmpDir := t.TempDir()
	wm, err := NewWorktreeManager(tmpDir)
	if err != nil {
		t.Fatalf("failed to create WorktreeManager: %v", err)
	}

	// Valid paths
	validPaths := []string{
		"main.go",
		"internal/pkg/file.go",
		filepath.Join(tmpDir, "src", "code.go"),
	}
	if err := wm.ValidateTargetPaths(validPaths); err != nil {
		t.Fatalf("expected valid paths to pass, got: %v", err)
	}

	// Adversarial paths
	invalidCases := []struct {
		name string
		path string
	}{
		{"parent directory escape", "../secret.txt"},
		{"nested traversal", "foo/../../bar"},
		{"absolute path outside root", "/etc/passwd"},
		{"double dot alone", ".."},
	}

	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			err := wm.ValidateTargetPaths([]string{tc.path})
			if err == nil {
				t.Fatalf("expected error for path %q, got nil", tc.path)
			}
			if !errors.Is(err, ErrIsolationCompromised) {
				t.Fatalf("expected ErrIsolationCompromised, got: %v", err)
			}
		})
	}
}

func TestWorktreeManager_PrepareAndClean(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	// Seed source project file
	if err := os.WriteFile(filepath.Join(tmpDir, "hello.txt"), []byte("marshal"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	wm, err := NewWorktreeManager(tmpDir)
	if err != nil {
		t.Fatalf("failed to create WorktreeManager: %v", err)
	}

	wtPath, err := wm.PrepareWorktree(ctx, "task-1", "run-1")
	if err != nil {
		t.Fatalf("PrepareWorktree failed: %v", err)
	}

	// Verify isolated worktree has hello.txt
	isolatedFile := filepath.Join(wtPath, "hello.txt")
	content, err := os.ReadFile(isolatedFile)
	if err != nil || string(content) != "marshal" {
		t.Fatalf("expected copied file in worktree, got err=%v, content=%q", err, string(content))
	}

	// Clean up
	if err := wm.CleanWorktree(ctx, wtPath); err != nil {
		t.Fatalf("CleanWorktree failed: %v", err)
	}

	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Fatalf("expected worktree to be removed, but it still exists")
	}
}

func TestWorktreeManager_ReconcileChanges_PermittedAndScopeEnforcement(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	_ = os.WriteFile(filepath.Join(tmpDir, "allowed.txt"), []byte("initial"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "unauthorized.txt"), []byte("initial"), 0644)

	wm, err := NewWorktreeManager(tmpDir)
	if err != nil {
		t.Fatalf("failed to create WorktreeManager: %v", err)
	}

	wtPath, err := wm.PrepareWorktree(ctx, "task-1", "run-1")
	if err != nil {
		t.Fatalf("PrepareWorktree failed: %v", err)
	}
	defer wm.CleanWorktree(ctx, wtPath)

	// Modify permitted file
	_ = os.WriteFile(filepath.Join(wtPath, "allowed.txt"), []byte("modified by worker"), 0644)

	// Reconcile with permittedFiles = ["allowed.txt"]
	modified, err := wm.ReconcileChanges(wtPath, []string{"allowed.txt"})
	if err != nil {
		t.Fatalf("expected reconcile to succeed, got: %v", err)
	}
	if len(modified) != 1 || modified[0] != "allowed.txt" {
		t.Fatalf("expected modified [allowed.txt], got: %v", modified)
	}

	// Verify change was applied to project root
	rootContent, _ := os.ReadFile(filepath.Join(tmpDir, "allowed.txt"))
	if string(rootContent) != "modified by worker" {
		t.Fatalf("expected root file updated, got: %q", string(rootContent))
	}

	// Now modify an unpermitted file in the worktree
	_ = os.WriteFile(filepath.Join(wtPath, "unauthorized.txt"), []byte("rogue worker write"), 0644)

	// Reconcile again with permittedFiles = ["allowed.txt"] -> MUST FAIL CLOSED
	_, err = wm.ReconcileChanges(wtPath, []string{"allowed.txt"})
	if err == nil {
		t.Fatalf("expected scope violation error, got nil")
	}
	if !errors.Is(err, ErrIsolationCompromised) {
		t.Fatalf("expected ErrIsolationCompromised, got: %v", err)
	}
}
