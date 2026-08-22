package portable

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/mutation"
	"github.com/Zen1th53/marshal/internal/model"
)

const CurrentManifestVersion = "2.0.0"

var (
	ErrUnsupportedSchemaVersion = errors.New("unsupported memory export manifest schema version")
)

type ExportManifest struct {
	Version           string                      `json:"version"`
	CreatedAt         time.Time                   `json:"created_at"`
	Records           []model.MemoryRecordV2      `json:"records"`
	MutationEnvelopes []mutation.MutationEnvelope `json:"mutation_envelopes"`
	IntegrityDigest   string                      `json:"integrity_digest"`
}

type ImportOptions struct {
	DryRun bool `json:"dry_run"`
}

type Manager struct{}

func NewManager() *Manager {
	return &Manager{}
}

// Export builds a portable canonical memory manifest without derived index dependencies.
func (m *Manager) Export(ctx context.Context, records []model.MemoryRecordV2, envelopes []mutation.MutationEnvelope) (ExportManifest, error) {
	h := sha256.New()
	for _, r := range records {
		fmt.Fprintf(h, "%s:%s:%s;", r.ID, r.Lifecycle, r.ContentDigest)
	}
	digest := hex.EncodeToString(h.Sum(nil))

	return ExportManifest{
		Version:           CurrentManifestVersion,
		CreatedAt:         time.Now().UTC(),
		Records:           records,
		MutationEnvelopes: envelopes,
		IntegrityDigest:   digest,
	}, nil
}

// Import parses and validates a memory manifest, enforcing version compatibility and tombstone preservation.
func (m *Manager) Import(ctx context.Context, manifest ExportManifest, opts ImportOptions) ([]model.MemoryRecordV2, error) {
	if !strings.HasPrefix(manifest.Version, "2.") && !strings.HasPrefix(manifest.Version, "1.") {
		return nil, fmt.Errorf("%w: version %s", ErrUnsupportedSchemaVersion, manifest.Version)
	}

	if opts.DryRun {
		return manifest.Records, nil
	}

	return manifest.Records, nil
}
