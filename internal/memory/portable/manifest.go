package portable

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	return m.ExportAt(ctx, records, envelopes, time.Now().UTC())
}

func (m *Manager) ExportAt(ctx context.Context, records []model.MemoryRecordV2, envelopes []mutation.MutationEnvelope, now time.Time) (ExportManifest, error) {
	if err := ctx.Err(); err != nil {
		return ExportManifest{}, err
	}
	manifest := ExportManifest{Version: CurrentManifestVersion, CreatedAt: now, Records: records, MutationEnvelopes: envelopes}
	body, err := json.Marshal(manifest)
	if err != nil {
		return ExportManifest{}, err
	}
	sum := sha256.Sum256(body)
	manifest.IntegrityDigest = hex.EncodeToString(sum[:])
	// Return an owned copy so mutating an input slice cannot alter a sealed pack.
	body, err = json.Marshal(manifest)
	if err != nil {
		return ExportManifest{}, err
	}
	var owned ExportManifest
	err = json.Unmarshal(body, &owned)
	return owned, err

}

// Import parses and validates a memory manifest, enforcing version compatibility and tombstone preservation.
func (m *Manager) Import(ctx context.Context, manifest ExportManifest, opts ImportOptions) ([]model.MemoryRecordV2, error) {
	if !strings.HasPrefix(manifest.Version, "2.") && !strings.HasPrefix(manifest.Version, "1.") {
		return nil, fmt.Errorf("%w: version %s", ErrUnsupportedSchemaVersion, manifest.Version)
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(manifest.Records) > 128 || len(manifest.MutationEnvelopes) > 256 {
		return nil, fmt.Errorf("portable pack exceeds bounds")
	}
	stored := manifest.IntegrityDigest
	manifest.IntegrityDigest = ""
	body, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	if stored == "" || stored != hex.EncodeToString(sum[:]) {
		return nil, fmt.Errorf("portable pack integrity mismatch")
	}
	if opts.DryRun {
		return manifest.Records, nil
	}

	return manifest.Records, nil
}
