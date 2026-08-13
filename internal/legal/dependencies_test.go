package legal

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFindModuleLicenses(t *testing.T) {
	tmpDir := t.TempDir()
	licPath := filepath.Join(tmpDir, "LICENSE")
	if err := os.WriteFile(licPath, []byte("MIT License\nCopyright (c) 2026"), 0644); err != nil {
		t.Fatal(err)
	}
	noticePath := filepath.Join(tmpDir, "NOTICE")
	if err := os.WriteFile(noticePath, []byte("Notice file"), 0644); err != nil {
		t.Fatal(err)
	}

	results := findModuleLicenses(tmpDir, "example.com/foo", "v1.0.0")
	if len(results) != 2 {
		t.Fatalf("expected 2 license files, got %d", len(results))
	}

	if results[0].Path != "third-party/dependencies/example.com_foo@v1.0.0/LICENSE" {
		t.Errorf("unexpected path: %s", results[0].Path)
	}
	if results[0].BlobSHA256 == "" {
		t.Error("expected non-empty SHA256")
	}
}

func TestCollectDependencyEvidence(t *testing.T) {
	repoDir := createTestRepo(t)
	ctx := context.Background()

	deps, err := CollectDependencyEvidence(ctx, repoDir)
	if err != nil {
		t.Fatalf("CollectDependencyEvidence failed: %v", err)
	}
	if deps == nil {
		t.Fatal("expected non-nil DependencyEvidence")
	}
}
