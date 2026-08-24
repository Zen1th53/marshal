package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/model"
)

// ImportProviderSessionHistory imports a verified provider-native history
// through its normalizing adapter. The resulting records remain agent-authority
// candidates and pass the same project authorization, secret firewall,
// canonical deduplication and derived-index path as interchange imports.
func (s *MemoryService) ImportProviderSessionHistory(ctx context.Context, principal authz.Principal, projectID string, format importer.ProviderFormat, history []byte, dryRun bool) (importer.ImportResult, error) {
	if err := ctx.Err(); err != nil {
		return importer.ImportResult{}, err
	}
	if s == nil || s.store == nil || s.sessionImporter == nil {
		return importer.ImportResult{}, fmt.Errorf("%w: session importer is unavailable", model.ErrUnavailable)
	}
	if strings.TrimSpace(projectID) == "" {
		return importer.ImportResult{}, fmt.Errorf("%w: project_id is required", model.ErrInvalid)
	}
	if err := s.authorizer.Authorize(ctx, principal, authz.ActionMemoryRemember, projectID, model.MemoryCandidate); err != nil {
		return importer.ImportResult{}, err
	}

	result, err := s.sessionImporter.ImportProviderHistory(ctx, projectID, format, history, dryRun)
	if err != nil {
		return importer.ImportResult{}, err
	}
	if dryRun {
		return result, nil
	}

	committed := make([]model.MemoryRecordV2, 0, len(result.ImportedRecords))
	for _, rec := range result.ImportedRecords {
		if existing, findErr := s.store.FindMemoryByDigest(ctx, projectID, rec.ContentDigest); findErr == nil && existing.ID != "" {
			result.SkippedCount++
			continue
		}
		if err := s.store.WriteMemoryV2(ctx, rec); err != nil {
			return importer.ImportResult{}, fmt.Errorf("persist imported record: %w", err)
		}
		if err := s.IndexRecord(ctx, rec); err != nil {
			return importer.ImportResult{}, fmt.Errorf("index imported record: %w", err)
		}
		committed = append(committed, rec)
	}
	result.ImportedRecords = committed
	return result, nil
}
