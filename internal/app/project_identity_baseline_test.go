package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This test documents the baseline behavior Process 02 must fix: a project
// that is moved to a new path becomes unopenable, because the runtime compares
// the stored repository path against the current one and treats any difference
// as a conflict.
//
// Moving a project directory is an ordinary thing to do — reorganizing a
// workspace, renaming a parent folder, restoring from a backup to a different
// location. None of those change which project it is.
func TestMovedProjectIsRejectedAtBaseline(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	original := repo.Path()

	if _, err := Bootstrap(ctx, original); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	runtime, err := Open(ctx, original)
	if err != nil {
		t.Fatalf("open before move: %v", err)
	}
	runtime.Close()

	// Move the project to a sibling directory. Everything about the project is
	// unchanged except where it lives.
	moved := filepath.Join(filepath.Dir(original), "moved-"+filepath.Base(original))
	if err := os.Rename(original, moved); err != nil {
		t.Skipf("cannot move the project in this environment: %v", err)
	}

	_, err = Open(ctx, moved)
	if err == nil {
		t.Skip("the moved project opened; this baseline defect is already fixed")
	}
	if !strings.Contains(err.Error(), "repository identity differs") {
		t.Fatalf("the move failed for an unexpected reason: %v", err)
	}
	t.Logf("baseline defect confirmed: a moved project cannot be opened: %v", err)
}
