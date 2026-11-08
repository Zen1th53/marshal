package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// MemoryOverviewLimits configures the maximum items returned in bounded slices.
type MemoryOverviewLimits struct {
	Queue   int
	Recalls int
}

// MemoryOverview represents the aggregated state for the MARSHAL TUI Memory view.
type MemoryOverview struct {
	Counts        MemoryOverviewCounts
	ReviewQueue   []MemoryReviewQueueItem
	EditionTags   MemoryEditionTags
	RecentRecalls MemoryRecentRecalls
	MCPTools      MemoryMCPTools
	Snapshots     MemorySnapshots
}

// MemoryOverviewCounts contains total record count and breakdown by (kind, lifecycle).
type MemoryOverviewCounts struct {
	Total      int
	ByKindLife map[string]map[string]int
}

// MemoryReviewQueueItem represents a candidate record awaiting review.
type MemoryReviewQueueItem struct {
	ID         string
	Kind       model.MemoryKind
	Title      string
	Confidence model.MemoryConfidence
	Authority  model.MemoryAuthority
	CreatedAt  *time.Time
}

// MemoryEditionTags contains counts of edition-tagged records and distinct area names.
type MemoryEditionTags struct {
	ExtremeCount   int
	CommunityCount int
	SharedCount    int
	ExtremeAreas   []string
	CommunityAreas []string
	SharedAreas    []string
}

// MemoryRecentRecalls contains the newest retrieval receipts and total count.
type MemoryRecentRecalls struct {
	TotalReceipts int
	Recent        []MemoryReceiptSummary
}

// MemoryReceiptSummary summarizes a single memory retrieval receipt.
type MemoryReceiptSummary struct {
	ReceiptID     string
	CallerID      string
	QueryDigest   string
	TaskID        string
	RunID         string
	Provider      string
	DeniedCount   int
	OutcomeStatus string
	CreatedAt     time.Time
}

// MemoryMCPTools contains tool call counts from gateway logs and extreme MCP stats.
type MemoryMCPTools struct {
	ToolCalls       map[string]int
	CandidatesCount int
	ProposalsCount  int
}

// MemorySnapshots contains snapshot count and the timestamp of the newest snapshot.
type MemorySnapshots struct {
	TotalSnapshots int
	NewestSnapshot *time.Time
}

// MemoryOverview returns an aggregated overview of the project's memory state.
func (s *Store) MemoryOverview(ctx context.Context, projectID string, limits MemoryOverviewLimits) (MemoryOverview, error) {
	if s == nil || s.db == nil {
		return MemoryOverview{}, fmt.Errorf("store database is not configured")
	}

	queueLimit := limits.Queue
	if queueLimit <= 0 {
		queueLimit = 20
	}
	recallsLimit := limits.Recalls
	if recallsLimit <= 0 {
		recallsLimit = 20
	}

	var overview MemoryOverview
	overview.Counts.ByKindLife = make(map[string]map[string]int)

	// 1. Counts by (kind, lifecycle) from memory_records_v2
	rows, err := s.db.QueryContext(ctx, `
		SELECT kind, lifecycle, COUNT(*)
		FROM memory_records_v2
		WHERE project_id = ?
		GROUP BY kind, lifecycle
	`, projectID)
	if err != nil {
		return MemoryOverview{}, fmt.Errorf("query memory overview counts: %w", err)
	}
	defer rows.Close()

	totalRecords := 0
	for rows.Next() {
		var kind, lifecycle string
		var count int
		if err := rows.Scan(&kind, &lifecycle, &count); err != nil {
			return MemoryOverview{}, fmt.Errorf("scan memory overview counts: %w", err)
		}
		if overview.Counts.ByKindLife[kind] == nil {
			overview.Counts.ByKindLife[kind] = make(map[string]int)
		}
		overview.Counts.ByKindLife[kind][lifecycle] = count
		totalRecords += count
	}
	if err := rows.Err(); err != nil {
		return MemoryOverview{}, fmt.Errorf("iterate memory overview counts: %w", err)
	}
	overview.Counts.Total = totalRecords

	// 2. ReviewQueue: candidate records ordered by created_at DESC
	qRows, err := s.db.QueryContext(ctx, `
		SELECT memory_id, kind, title, confidence, authority, created_at
		FROM memory_records_v2
		WHERE project_id = ? AND lifecycle = 'candidate'
		ORDER BY created_at DESC
		LIMIT ?
	`, projectID, queueLimit)
	if err != nil {
		return MemoryOverview{}, fmt.Errorf("query memory review queue: %w", err)
	}
	defer qRows.Close()

	for qRows.Next() {
		var item MemoryReviewQueueItem
		var kindStr, confStr, authStr, createdStr string
		if err := qRows.Scan(&item.ID, &kindStr, &item.Title, &confStr, &authStr, &createdStr); err != nil {
			return MemoryOverview{}, fmt.Errorf("scan memory review queue: %w", err)
		}
		item.Kind = model.MemoryKind(kindStr)
		item.Confidence = model.MemoryConfidence(confStr)
		item.Authority = model.MemoryAuthority(authStr)
		if createdStr != "" {
			if t, err := time.Parse(time.RFC3339Nano, createdStr); err == nil {
				item.CreatedAt = &t
			} else if t, err := time.Parse(time.RFC3339, createdStr); err == nil {
				item.CreatedAt = &t
			}
		}
		overview.ReviewQueue = append(overview.ReviewQueue, item)
	}
	if err := qRows.Err(); err != nil {
		return MemoryOverview{}, fmt.Errorf("iterate memory review queue: %w", err)
	}

	// 3. EditionTags: titles starting with [extreme/, [community/, [shared/
	// Extract edition prefix and area name between '/' and ']'
	tagRows, err := s.db.QueryContext(ctx, `
		SELECT
			CASE
				WHEN title LIKE '[extreme/%' THEN 'extreme'
				WHEN title LIKE '[community/%' THEN 'community'
				WHEN title LIKE '[shared/%' THEN 'shared'
			END AS edition,
			CASE
				WHEN instr(title, ']') > instr(title, '/')
				THEN substr(title, instr(title, '/') + 1, instr(title, ']') - instr(title, '/') - 1)
				ELSE ''
			END AS area
		FROM memory_records_v2
		WHERE project_id = ? AND (
			title LIKE '[extreme/%' OR
			title LIKE '[community/%' OR
			title LIKE '[shared/%'
		)
	`, projectID)
	if err != nil {
		return MemoryOverview{}, fmt.Errorf("query memory edition tags: %w", err)
	}
	defer tagRows.Close()

	extremeAreas := make(map[string]bool)
	communityAreas := make(map[string]bool)
	sharedAreas := make(map[string]bool)

	for tagRows.Next() {
		var edition, area string
		if err := tagRows.Scan(&edition, &area); err != nil {
			return MemoryOverview{}, fmt.Errorf("scan memory edition tags: %w", err)
		}
		switch edition {
		case "extreme":
			overview.EditionTags.ExtremeCount++
			if area != "" && !extremeAreas[area] {
				extremeAreas[area] = true
				overview.EditionTags.ExtremeAreas = append(overview.EditionTags.ExtremeAreas, area)
			}
		case "community":
			overview.EditionTags.CommunityCount++
			if area != "" && !communityAreas[area] {
				communityAreas[area] = true
				overview.EditionTags.CommunityAreas = append(overview.EditionTags.CommunityAreas, area)
			}
		case "shared":
			overview.EditionTags.SharedCount++
			if area != "" && !sharedAreas[area] {
				sharedAreas[area] = true
				overview.EditionTags.SharedAreas = append(overview.EditionTags.SharedAreas, area)
			}
		}
	}
	if err := tagRows.Err(); err != nil {
		return MemoryOverview{}, fmt.Errorf("iterate memory edition tags: %w", err)
	}

	// 4. RecentRecalls: total count and up to limits.Recalls newest rows
	var totalReceipts int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM memory_retrieval_receipts
		WHERE project_id = ?
	`, projectID).Scan(&totalReceipts); err != nil {
		return MemoryOverview{}, fmt.Errorf("count memory retrieval receipts: %w", err)
	}
	overview.RecentRecalls.TotalReceipts = totalReceipts

	rcRows, err := s.db.QueryContext(ctx, `
		SELECT receipt_id, caller_id, query_digest, task_id, run_id, provider, denied_count, outcome_status, created_at
		FROM memory_retrieval_receipts
		WHERE project_id = ?
		ORDER BY created_at DESC
		LIMIT ?
	`, projectID, recallsLimit)
	if err != nil {
		return MemoryOverview{}, fmt.Errorf("query memory retrieval receipts: %w", err)
	}
	defer rcRows.Close()

	for rcRows.Next() {
		var item MemoryReceiptSummary
		var createdAtStr string
		if err := rcRows.Scan(
			&item.ReceiptID,
			&item.CallerID,
			&item.QueryDigest,
			&item.TaskID,
			&item.RunID,
			&item.Provider,
			&item.DeniedCount,
			&item.OutcomeStatus,
			&createdAtStr,
		); err != nil {
			return MemoryOverview{}, fmt.Errorf("scan memory retrieval receipt: %w", err)
		}
		if t, err := time.Parse(time.RFC3339Nano, createdAtStr); err == nil {
			item.CreatedAt = t
		} else if t, err := time.Parse(time.RFC3339, createdAtStr); err == nil {
			item.CreatedAt = t
		}
		overview.RecentRecalls.Recent = append(overview.RecentRecalls.Recent, item)
	}
	if err := rcRows.Err(); err != nil {
		return MemoryOverview{}, fmt.Errorf("iterate memory retrieval receipts: %w", err)
	}

	// 5. MCPTools: call counts grouped by tool name from mcp_gateway_logs
	overview.MCPTools.ToolCalls = make(map[string]int)
	mcpRows, err := s.db.QueryContext(ctx, `
		SELECT m.tool, COUNT(*)
		FROM mcp_gateway_logs m
		INNER JOIN tasks t ON t.task_id = m.task_id
		WHERE t.project_id = ?
		GROUP BY m.tool
	`, projectID)
	if err == nil {
		defer mcpRows.Close()
		for mcpRows.Next() {
			var tool string
			var count int
			if err := mcpRows.Scan(&tool, &count); err == nil {
				overview.MCPTools.ToolCalls[tool] = count
			}
		}
	}

	// Candidates and proposals counts (edition-dependent: extreme vs community)
	candCount, propCount, err := s.extremeMCPOverviewCounts(ctx, projectID)
	if err != nil {
		return MemoryOverview{}, err
	}
	overview.MCPTools.CandidatesCount = candCount
	overview.MCPTools.ProposalsCount = propCount

	// 6. Snapshots: count of memory_restore_snapshots and the newest snapshot time
	var snapCount int
	var newestSnapStr sql.NullString
	err = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*), MAX(created_at)
		FROM memory_restore_snapshots
		WHERE project_id = ?
	`, projectID).Scan(&snapCount, &newestSnapStr)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return MemoryOverview{}, fmt.Errorf("query memory restore snapshots: %w", err)
	}
	overview.Snapshots.TotalSnapshots = snapCount
	if newestSnapStr.Valid && newestSnapStr.String != "" {
		if t, err := time.Parse(time.RFC3339Nano, newestSnapStr.String); err == nil {
			overview.Snapshots.NewestSnapshot = &t
		} else if t, err := time.Parse(time.RFC3339, newestSnapStr.String); err == nil {
			overview.Snapshots.NewestSnapshot = &t
		}
	}

	return overview, nil
}
