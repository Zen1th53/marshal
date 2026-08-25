package legal

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestGitEvidenceReadsCommittedBlobNotDirtyWorkingTree(t *testing.T) {
	repoDir := createTestRepo(t)
	ctx := context.Background()

	source, err := CollectSourceEvidence(ctx, repoDir)
	if err != nil {
		t.Fatalf("CollectSourceEvidence failed: %v", err)
	}
	if !source.WorkingTreeClean {
		t.Error("expected initial repository working tree to be clean")
	}
	if source.RuntimeVersion != "1.0.1" {
		t.Errorf("runtime version = %q, want 1.0.1", source.RuntimeVersion)
	}
	if source.PackVersion != "6.0.0" {
		t.Errorf("pack version = %q, want 6.0.0", source.PackVersion)
	}

	origBlob, err := ReadBlob(ctx, repoDir, source.HeadSHA, "LICENSE")
	if err != nil {
		t.Fatalf("ReadBlob failed: %v", err)
	}
	if string(origBlob) != "GNU AFFERO GENERAL PUBLIC LICENSE\nVersion 3" {
		t.Errorf("unexpected initial blob content: %q", string(origBlob))
	}

	// Dirty the working tree file without committing
	dirtyPath := filepath.Join(repoDir, "LICENSE")
	if err := os.WriteFile(dirtyPath, []byte("DIRTY UNCOMMITTED CONTENT"), 0644); err != nil {
		t.Fatalf("failed to dirty LICENSE file: %v", err)
	}

	// Re-check source evidence
	sourceAfter, err := CollectSourceEvidence(ctx, repoDir)
	if err != nil {
		t.Fatalf("CollectSourceEvidence after dirtied working tree failed: %v", err)
	}
	if sourceAfter.WorkingTreeClean {
		t.Error("expected working tree to be reported as NOT clean")
	}

	// ReadBlob MUST still return the committed blob content, NOT the dirty working tree content
	blobAfter, err := ReadBlob(ctx, repoDir, sourceAfter.HeadSHA, "LICENSE")
	if err != nil {
		t.Fatalf("ReadBlob after dirtied working tree failed: %v", err)
	}
	if string(blobAfter) != "GNU AFFERO GENERAL PUBLIC LICENSE\nVersion 3" {
		t.Errorf("CRITICAL REGRESSION: ReadBlob returned dirty content %q instead of committed blob", string(blobAfter))
	}
}

func TestCommitAncestryCollection(t *testing.T) {
	repoDir := createTestRepo(t)
	ctx := context.Background()

	source, err := CollectSourceEvidence(ctx, repoDir)
	if err != nil {
		t.Fatalf("CollectSourceEvidence failed: %v", err)
	}

	commits, authors, err := CollectCommitAncestry(ctx, repoDir, source.HeadSHA)
	if err != nil {
		t.Fatalf("CollectCommitAncestry failed: %v", err)
	}

	if len(commits) != 1 {
		t.Errorf("expected 1 commit, got %d", len(commits))
	}
	if len(authors) != 1 {
		t.Errorf("expected 1 author, got %d", len(authors))
	}
	if authors[0].Name != "Test Author" {
		t.Errorf("expected author 'Test Author', got %q", authors[0].Name)
	}
}
