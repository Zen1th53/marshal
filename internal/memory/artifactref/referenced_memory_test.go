package artifactref_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/artifactref"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT156MultimodalArtifactReferencedMemory(t *testing.T) {
	ctx := context.Background()
	mgr := artifactref.NewManager()

	// 1. Register artifact (Screenshot of UI layout failure)
	art := artifactref.ArtifactRef{
		Digest:           "sha256:d8a94b54e7d9834190c1f21132a0d924",
		Path:             "artifacts/ui_error_screenshot.png",
		Kind:             artifactref.ArtifactImageScreenshot,
		ScopeID:          "tenant-alpha",
		ExtractorVersion: "ocr-v1.2",
	}
	mgr.RegisterArtifact(art)

	// 2. Create memory observation bound to artifact
	rec := model.MemoryRecordV2{
		ID:        "MEM-UI-OBS-01",
		ScopeID:   "tenant-alpha",
		Title:     "UI Alignment Bug",
		Body:      "Button overflows container on 1080p resolution",
		Lifecycle: model.MemoryDurable,
	}

	boundRec, err := mgr.BindMemoryToArtifact(ctx, rec, art.Digest)
	if err != nil {
		t.Fatalf("BindMemoryToArtifact: %v", err)
	}
	if boundRec.ArtifactDigest != art.Digest {
		t.Fatalf("expected artifact digest bound to memory record, got: %s", boundRec.ArtifactDigest)
	}

	// 3. Artifact replacement / digest mismatch stales derived observation
	err = mgr.ValidateArtifactIntegrity(ctx, boundRec, "sha256:forged_modified_content")
	if !errors.Is(err, artifactref.ErrArtifactDigestMismatch) {
		t.Fatalf("expected ErrArtifactDigestMismatch when artifact content changes, got: %v", err)
	}

	// 4. Unauthorized cross-scope artifact expansion is denied
	_, err = mgr.ExpandArtifact(ctx, art.Digest, "tenant-unauthorized-beta")
	if !errors.Is(err, artifactref.ErrUnauthorizedArtifactAccess) {
		t.Fatalf("expected ErrUnauthorizedArtifactAccess for cross-tenant access, got: %v", err)
	}
}
