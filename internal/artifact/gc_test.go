package artifact

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

type dummyRegistrar struct {
	artifacts []model.Artifact
}

func (d *dummyRegistrar) RegisterArtifact(_ context.Context, a model.Artifact) error {
	d.artifacts = append(d.artifacts, a)
	return nil
}

func TestArtifactGCLifecycle(t *testing.T) {
	root := filepath.Join(t.TempDir(), "artifacts")
	reg := &dummyRegistrar{}
	st := New(root, reg)
	ctx := context.Background()

	// 1. Put two artifacts
	art1, err := st.Put(ctx, model.ArtifactInput{
		ProjectID:    "PRJ-ART",
		Kind:         "report",
		SourceCommit: "commit1",
		Data:         bytes.NewReader([]byte("referenced artifact data")),
	})
	if err != nil {
		t.Fatal(err)
	}

	art2, err := st.Put(ctx, model.ArtifactInput{
		ProjectID:    "PRJ-ART",
		Kind:         "binary",
		SourceCommit: "commit2",
		Data:         bytes.NewReader([]byte("orphan artifact data to clean")),
	})
	if err != nil {
		t.Fatal(err)
	}

	// 2. Run GC with dry-run mode (art1 is referenced, art2 is orphan)
	dryRes, err := st.GC(ctx, GCRequest{
		DryRun:            true,
		TTL:               0, // immediate expiration for orphan
		ReferencedDigests: []string{art1.Digest},
	})
	if err != nil {
		t.Fatalf("dry-run GC failed: %v", err)
	}

	if len(dryRes.OrphanFiles) != 1 || dryRes.OrphanFiles[0] != art2.Path {
		t.Fatalf("expected art2 to be detected as orphan, got: %+v", dryRes.OrphanFiles)
	}
	if len(dryRes.RetainedFiles) != 1 || dryRes.RetainedFiles[0] != art1.Path {
		t.Fatalf("expected art1 to be retained, got: %+v", dryRes.RetainedFiles)
	}

	// Verify both files still exist on disk after dry run
	if _, err := os.Stat(art1.Path); err != nil {
		t.Fatalf("art1 was deleted during dry run: %v", err)
	}
	if _, err := os.Stat(art2.Path); err != nil {
		t.Fatalf("art2 was deleted during dry run: %v", err)
	}

	// 3. Run Real GC
	realRes, err := st.GC(ctx, GCRequest{
		DryRun:            false,
		TTL:               0,
		ReferencedDigests: []string{art1.Digest},
	})
	if err != nil {
		t.Fatalf("real GC failed: %v", err)
	}

	// art1 must still exist
	if _, err := os.Stat(art1.Path); err != nil {
		t.Fatalf("referenced art1 was deleted: %v", err)
	}

	// art2 (orphan) must be deleted
	if _, err := os.Stat(art2.Path); !os.IsNotExist(err) {
		t.Fatalf("orphan art2 was not deleted, exists: %s", art2.Path)
	}

	_ = realRes
}
