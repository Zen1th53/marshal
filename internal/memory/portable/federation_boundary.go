package portable

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/model"
)

var (
	ErrTamperedPack       = errors.New("federation pack integrity check failed: digest mismatch")
	ErrUntrustedAuthority = errors.New("untrusted foreign authority signature")
	ErrExportRejected     = errors.New("export rejected: private or secret content detected")
)

type ExportFilter struct {
	MinimumAuthority  model.MemoryAuthority  `json:"minimum_authority,omitempty"`
	MinimumConfidence model.MemoryConfidence `json:"minimum_confidence,omitempty"`
	ExcludePrivate    bool                   `json:"exclude_private"`
}

type FederatedPack struct {
	Version         string                 `json:"version"`
	SourceProjectID string                 `json:"source_project_id"`
	ExportedAt      time.Time              `json:"exported_at"`
	Records         []model.MemoryRecordV2 `json:"records"`
	IntegrityDigest string                 `json:"integrity_digest"`
	Signature       string                 `json:"signature,omitempty"`
}

type FederationImportOptions struct {
	DryRun           bool     `json:"dry_run"`
	TrustedKeyIDs    []string `json:"trusted_key_ids,omitempty"`
	AllowDurableOnly bool     `json:"allow_durable_only"`
}

type FederationImportResult struct {
	ImportedRecords []model.MemoryRecordV2 `json:"imported_records"`
	SkippedCount    int                    `json:"skipped_count"`
	DryRun          bool                   `json:"dry_run"`
}

type FederationBoundary struct {
	firewall *security.Firewall
}

func NewFederationBoundary(fw *security.Firewall) *FederationBoundary {
	if fw == nil {
		fw = security.NewFirewall(security.FirewallConfig{})
	}
	return &FederationBoundary{firewall: fw}
}

func computePackDigest(records []model.MemoryRecordV2) string {
	h := sha256.New()
	sortedRecs := make([]model.MemoryRecordV2, len(records))
	copy(sortedRecs, records)
	sort.Slice(sortedRecs, func(i, j int) bool {
		return sortedRecs[i].ID < sortedRecs[j].ID
	})
	for _, r := range sortedRecs {
		fmt.Fprintf(h, "%s:%s:%s:%s:%s\n", r.ID, r.Kind, r.Title, r.Body, r.ContentDigest)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ExportPack produces a sanitized, environment-neutral, secret-free knowledge pack.
func (fb *FederationBoundary) ExportPack(ctx context.Context, projectID string, records []model.MemoryRecordV2, filter ExportFilter) (FederatedPack, error) {
	if err := ctx.Err(); err != nil {
		return FederatedPack{}, err
	}

	var sanitized []model.MemoryRecordV2
	for _, r := range records {
		// 1. Enforce strict private scope omission
		if r.ProjectID != projectID || r.Scope != string(model.ScopeProject) || r.ScopeID != projectID || r.ACLScope != "" {
			continue
		}
		if r.Lifecycle == model.MemoryTombstoned || r.Lifecycle == model.MemoryRejected {
			continue
		}
		if filter.MinimumAuthority != "" && authorityTier(r.Authority) < authorityTier(filter.MinimumAuthority) {
			continue
		}
		if filter.MinimumConfidence != "" && confidenceTier(r.Confidence) < confidenceTier(filter.MinimumConfidence) {
			continue
		}

		// 2. Scan through security firewall
		if err := fb.firewall.ScanRecord(ctx, r); err != nil {
			return FederatedPack{}, fmt.Errorf("%w: record %s contains secret material", ErrExportRejected, r.ID)
		}

		// 3. Strip environment-specific and local worktree anchors
		clean := r
		clean.WorktreeID = ""
		if clean.ExtMeta != nil {
			cleanMeta := make(map[string]any)
			for k, v := range clean.ExtMeta {
				if k != "local_worktree" && k != "local_socket" && k != "tombstone_reason" {
					cleanMeta[k] = v
				}
			}
			clean.ExtMeta = cleanMeta
		}
		sanitized = append(sanitized, clean)
	}

	digest := computePackDigest(sanitized)
	return FederatedPack{
		Version:         CurrentManifestVersion,
		SourceProjectID: projectID,
		ExportedAt:      time.Now().UTC(),
		Records:         sanitized,
		IntegrityDigest: digest,
	}, nil
}

// ImportPack validates pack integrity and maps foreign authority classes to untrusted candidate defaults.
func (fb *FederationBoundary) ImportPack(ctx context.Context, targetProjectID string, pack FederatedPack, opts FederationImportOptions) (FederationImportResult, error) {
	if err := ctx.Err(); err != nil {
		return FederationImportResult{}, err
	}
	if !strings.HasPrefix(pack.Version, "2.") && !strings.HasPrefix(pack.Version, "1.") {
		return FederationImportResult{}, fmt.Errorf("%w: pack version %s", ErrUnsupportedSchemaVersion, pack.Version)
	}
	if strings.TrimSpace(targetProjectID) == "" || strings.TrimSpace(pack.SourceProjectID) == "" {
		return FederationImportResult{}, model.ErrInvalid
	}
	// Signed federation is deliberately disabled until a verifier, replay
	// protection, and peer trust store are configured. Never pretend a caller-
	// supplied key ID verifies a signature.
	if pack.Signature != "" || len(opts.TrustedKeyIDs) > 0 {
		return FederationImportResult{}, ErrUntrustedAuthority
	}

	// 1. Integrity check
	expectedDigest := computePackDigest(pack.Records)
	if pack.IntegrityDigest != expectedDigest {
		return FederationImportResult{}, ErrTamperedPack
	}

	var prepared []model.MemoryRecordV2
	now := time.Now().UTC()

	for _, r := range pack.Records {
		if r.ProjectID != pack.SourceProjectID || r.Scope != string(model.ScopeProject) || r.ScopeID != pack.SourceProjectID || r.ACLScope != "" {
			return FederationImportResult{}, fmt.Errorf("%w: foreign record attempts visibility widening", ErrExportRejected)
		}
		if opts.AllowDurableOnly && r.Lifecycle != model.MemoryDurable {
			continue
		}
		// 2. Security scan on every incoming record
		if err := fb.firewall.ScanRecord(ctx, r); err != nil {
			return FederationImportResult{}, fmt.Errorf("incoming record %s failed security scan: %w", r.ID, err)
		}

		// 3. Foreign authority downgrade: incoming records are strictly candidate-only agent authority
		mapped := r
		mapped.ID = federatedRecordID(pack.SourceProjectID, r.ID, r.ContentDigest)
		mapped.ProjectID = targetProjectID
		mapped.Authority = model.AuthorityAgent
		mapped.Lifecycle = model.MemoryCandidate
		mapped.Confidence = model.ConfidenceInferred
		mapped.WorktreeID = ""
		mapped.ACLScope = ""
		mapped.IngestedAt = now
		mapped.UpdatedAt = now

		// Scope mapping to project scope
		mapped.Scope = string(model.ScopeProject)
		mapped.ScopeID = targetProjectID
		mapped.Source = model.MemorySource{Kind: "federation_import", Reference: pack.SourceProjectID + ":" + r.ID, AgentID: r.Source.AgentID}
		if mapped.ExtMeta == nil {
			mapped.ExtMeta = map[string]any{}
		}
		mapped.ExtMeta["foreign_memory_id"] = r.ID
		mapped.ExtMeta["foreign_project_id"] = pack.SourceProjectID
		mapped.ExtMeta["foreign_lifecycle"] = string(r.Lifecycle)

		prepared = append(prepared, mapped)
	}

	return FederationImportResult{
		ImportedRecords: prepared,
		SkippedCount:    0,
		DryRun:          opts.DryRun,
	}, nil
}

func federatedRecordID(sourceProjectID, sourceMemoryID, digest string) string {
	h := sha256.Sum256([]byte(sourceProjectID + "\x00" + sourceMemoryID + "\x00" + digest))
	return "MEM-FED-" + hex.EncodeToString(h[:])[:24]
}

func authorityTier(authority model.MemoryAuthority) int {
	switch authority {
	case model.AuthorityOperator:
		return 4
	case model.AuthorityPolicy:
		return 3
	case model.AuthorityVerified:
		return 2
	case model.AuthorityAgent:
		return 1
	default:
		return 0
	}
}

func confidenceTier(confidence model.MemoryConfidence) int {
	switch confidence {
	case model.ConfidenceVerified:
		return 4
	case model.ConfidenceObserved:
		return 3
	case model.ConfidenceInferred:
		return 2
	case model.ConfidenceUnverified:
		return 1
	default:
		return 0
	}
}
