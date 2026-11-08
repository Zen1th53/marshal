//go:build !governed

package store

import (
	"context"
	"fmt"
)

func (s *Store) extremeMCPOverviewCounts(ctx context.Context, projectID string) (int, int, error) {
	var candidatesCount int
	var hasCandidates bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='extreme_mcp_memory_candidates')`).Scan(&hasCandidates); err != nil {
		return 0, 0, fmt.Errorf("check extreme_mcp_memory_candidates: %w", err)
	}
	if hasCandidates {
		err := s.db.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM extreme_mcp_memory_candidates
			WHERE project_id = ?
		`, projectID).Scan(&candidatesCount)
		if err != nil {
			return 0, 0, fmt.Errorf("count Marshal MCP memory candidates: %w", err)
		}
	}

	var proposalsCount int
	var hasProposals bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='extreme_mcp_proposals')`).Scan(&hasProposals); err != nil {
		return 0, 0, fmt.Errorf("check extreme_mcp_proposals: %w", err)
	}
	if hasProposals {
		err := s.db.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM extreme_mcp_proposals
			WHERE project_id = ?
		`, projectID).Scan(&proposalsCount)
		if err != nil {
			return 0, 0, fmt.Errorf("count Marshal MCP proposals: %w", err)
		}
	}

	return candidatesCount, proposalsCount, nil
}
