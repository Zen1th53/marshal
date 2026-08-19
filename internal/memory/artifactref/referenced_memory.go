package artifactref

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Zen1th53/marshal/internal/model"
)

var (
	ErrArtifactNotFound           = errors.New("artifact not found")
	ErrArtifactDigestMismatch     = errors.New("artifact digest mismatch: source artifact has mutated since memory extraction")
	ErrUnauthorizedArtifactAccess = errors.New("unauthorized artifact access: scope mismatch")
)

type ArtifactKind string

const (
	ArtifactImageScreenshot ArtifactKind = "IMAGE_SCREENSHOT"
	ArtifactDiagramSVG      ArtifactKind = "DIAGRAM_SVG"
	ArtifactBuildLog        ArtifactKind = "BUILD_LOG"
	ArtifactPatchDiff       ArtifactKind = "PATCH_DIFF"
	ArtifactBinaryFixture   ArtifactKind = "BINARY_FIXTURE"
)

type ArtifactRef struct {
	Digest           string       `json:"digest"`
	Path             string       `json:"path"`
	Kind             ArtifactKind `json:"kind"`
	ScopeID          string       `json:"scope_id"`
	ExtractorVersion string       `json:"extractor_version"`
}

type BoundMemoryRecord struct {
	model.MemoryRecordV2
	ArtifactDigest string `json:"artifact_digest"`
}

type Manager struct {
	mu        sync.RWMutex
	artifacts map[string]ArtifactRef
}

func NewManager() *Manager {
	return &Manager{
		artifacts: make(map[string]ArtifactRef),
	}
}

func (m *Manager) RegisterArtifact(art ArtifactRef) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.artifacts[art.Digest] = art
}

// BindMemoryToArtifact binds a content-addressed artifact reference to a canonical memory record.
func (m *Manager) BindMemoryToArtifact(ctx context.Context, rec model.MemoryRecordV2, artifactDigest string) (BoundMemoryRecord, error) {
	m.mu.RLock()
	art, ok := m.artifacts[artifactDigest]
	m.mu.RUnlock()

	if !ok {
		return BoundMemoryRecord{}, ErrArtifactNotFound
	}
	if rec.ScopeID != "" && art.ScopeID != "" && rec.ScopeID != art.ScopeID {
		return BoundMemoryRecord{}, ErrUnauthorizedArtifactAccess
	}

	return BoundMemoryRecord{
		MemoryRecordV2: rec,
		ArtifactDigest: artifactDigest,
	}, nil
}

// ValidateArtifactIntegrity checks whether the referenced artifact on disk still matches the original digest.
func (m *Manager) ValidateArtifactIntegrity(ctx context.Context, rec BoundMemoryRecord, currentDiskDigest string) error {
	if rec.ArtifactDigest != currentDiskDigest {
		return fmt.Errorf("%w: expected %s, got %s", ErrArtifactDigestMismatch, rec.ArtifactDigest, currentDiskDigest)
	}
	return nil
}

// ExpandArtifact retrieves the artifact metadata after verifying caller scope permission.
func (m *Manager) ExpandArtifact(ctx context.Context, artifactDigest string, callerScopeID string) (ArtifactRef, error) {
	m.mu.RLock()
	art, ok := m.artifacts[artifactDigest]
	m.mu.RUnlock()

	if !ok {
		return ArtifactRef{}, ErrArtifactNotFound
	}
	if art.ScopeID != "" && callerScopeID != art.ScopeID {
		return ArtifactRef{}, ErrUnauthorizedArtifactAccess
	}

	return art, nil
}
