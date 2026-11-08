package store

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func setupTestStore(t *testing.T, projectID string) *Store {
	t.Helper()
	ctx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate store: %v", err)
	}

	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO projects(project_id, repository, default_branch, pack_version, created_at)
		VALUES(?, '/repo', 'main', '1.0', ?)
	`, projectID, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert project: %v", err)
	}

	return st
}

func TestMemoryOverviewEmptyStore(t *testing.T) {
	st := setupTestStore(t, "proj-empty")
	ctx := t.Context()

	overview, err := st.MemoryOverview(ctx, "proj-empty", MemoryOverviewLimits{Queue: 10, Recalls: 10})
	if err != nil {
		t.Fatalf("MemoryOverview failed: %v", err)
	}

	if overview.Counts.Total != 0 {
		t.Errorf("expected 0 total records, got %d", overview.Counts.Total)
	}
	if len(overview.Counts.ByKindLife) != 0 {
		t.Errorf("expected empty ByKindLife, got %+v", overview.Counts.ByKindLife)
	}
	if len(overview.ReviewQueue) != 0 {
		t.Errorf("expected empty ReviewQueue, got %d items", len(overview.ReviewQueue))
	}
	if overview.EditionTags.ExtremeCount != 0 || overview.EditionTags.CommunityCount != 0 || overview.EditionTags.SharedCount != 0 {
		t.Errorf("expected 0 edition counts, got %+v", overview.EditionTags)
	}
	if len(overview.EditionTags.ExtremeAreas) != 0 || len(overview.EditionTags.CommunityAreas) != 0 || len(overview.EditionTags.SharedAreas) != 0 {
		t.Errorf("expected empty edition areas, got %+v", overview.EditionTags)
	}
	if overview.RecentRecalls.TotalReceipts != 0 || len(overview.RecentRecalls.Recent) != 0 {
		t.Errorf("expected 0 receipts, got %+v", overview.RecentRecalls)
	}
	if overview.Snapshots.TotalSnapshots != 0 || overview.Snapshots.NewestSnapshot != nil {
		t.Errorf("expected 0 snapshots, got %+v", overview.Snapshots)
	}
}

func TestMemoryOverviewMixedKindsLifecycles(t *testing.T) {
	st := setupTestStore(t, "proj-mixed")
	ctx := t.Context()

	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO projects(project_id, repository, default_branch, pack_version, created_at)
		VALUES('proj-other', '/repo-other', 'main', '1.0', ?)
	`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert other project: %v", err)
	}

	baseTime := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	records := []model.MemoryRecordV2{
		{
			ID:         "mem-1",
			ProjectID:  "proj-mixed",
			Kind:       model.MemoryKindSemantic,
			Lifecycle:  model.MemoryDurable,
			Confidence: model.ConfidenceVerified,
			Authority:  model.AuthorityOperator,
			Title:      "Convention 1",
			Body:       "Body 1",
			Scope:      "project",
			ScopeID:    "proj-mixed",
			ObservedAt: baseTime,
			IngestedAt: baseTime,
			ValidFrom:  baseTime,
			CreatedAt:  baseTime,
		},
		{
			ID:         "mem-2",
			ProjectID:  "proj-mixed",
			Kind:       model.MemoryKindSemantic,
			Lifecycle:  model.MemoryCandidate,
			Confidence: model.ConfidenceObserved,
			Authority:  model.AuthorityAgent,
			Title:      "Convention 2",
			Body:       "Body 2",
			Scope:      "project",
			ScopeID:    "proj-mixed",
			ObservedAt: baseTime.Add(time.Minute),
			IngestedAt: baseTime.Add(time.Minute),
			ValidFrom:  baseTime.Add(time.Minute),
			CreatedAt:  baseTime.Add(time.Minute),
		},
		{
			ID:         "mem-3",
			ProjectID:  "proj-mixed",
			Kind:       model.MemoryKindFailure,
			Lifecycle:  model.MemoryCandidate,
			Confidence: model.ConfidenceInferred,
			Authority:  model.AuthorityAgent,
			Title:      "Failure 1",
			Body:       "Body 3",
			Scope:      "project",
			ScopeID:    "proj-mixed",
			ObservedAt: baseTime.Add(2 * time.Minute),
			IngestedAt: baseTime.Add(2 * time.Minute),
			ValidFrom:  baseTime.Add(2 * time.Minute),
			CreatedAt:  baseTime.Add(2 * time.Minute),
		},
		{
			ID:         "mem-4",
			ProjectID:  "proj-other", // different project, should not be included
			Kind:       model.MemoryKindSemantic,
			Lifecycle:  model.MemoryDurable,
			Confidence: model.ConfidenceVerified,
			Authority:  model.AuthorityOperator,
			Title:      "Other project record",
			Body:       "Body other",
			Scope:      "project",
			ScopeID:    "proj-other",
			ObservedAt: baseTime,
			IngestedAt: baseTime,
			ValidFrom:  baseTime,
			CreatedAt:  baseTime,
		},
	}

	for _, r := range records {
		if err := st.WriteMemoryV2(ctx, r); err != nil {
			t.Fatalf("WriteMemoryV2 failed: %v", err)
		}
	}

	overview, err := st.MemoryOverview(ctx, "proj-mixed", MemoryOverviewLimits{Queue: 10, Recalls: 10})
	if err != nil {
		t.Fatalf("MemoryOverview failed: %v", err)
	}

	if overview.Counts.Total != 3 {
		t.Errorf("expected 3 total records for proj-mixed, got %d", overview.Counts.Total)
	}
	if overview.Counts.ByKindLife[string(model.MemoryKindSemantic)][string(model.MemoryDurable)] != 1 {
		t.Errorf("expected 1 durable semantic, got %d", overview.Counts.ByKindLife[string(model.MemoryKindSemantic)][string(model.MemoryDurable)])
	}
	if overview.Counts.ByKindLife[string(model.MemoryKindSemantic)][string(model.MemoryCandidate)] != 1 {
		t.Errorf("expected 1 candidate semantic, got %d", overview.Counts.ByKindLife[string(model.MemoryKindSemantic)][string(model.MemoryCandidate)])
	}
	if overview.Counts.ByKindLife[string(model.MemoryKindFailure)][string(model.MemoryCandidate)] != 1 {
		t.Errorf("expected 1 candidate failure, got %d", overview.Counts.ByKindLife[string(model.MemoryKindFailure)][string(model.MemoryCandidate)])
	}
}

func TestMemoryOverviewCandidateQueueOrderingAndLimit(t *testing.T) {
	st := setupTestStore(t, "proj-queue")
	ctx := t.Context()

	baseTime := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	for i := 1; i <= 5; i++ {
		rec := model.MemoryRecordV2{
			ID:         fmt.Sprintf("cand-%d", i),
			ProjectID:  "proj-queue",
			Kind:       model.MemoryKindDecision,
			Lifecycle:  model.MemoryCandidate,
			Confidence: model.ConfidenceObserved,
			Authority:  model.AuthorityAgent,
			Title:      fmt.Sprintf("Candidate %d", i),
			Body:       fmt.Sprintf("Body %d", i),
			Scope:      "project",
			ScopeID:    "proj-queue",
			ObservedAt: baseTime.Add(time.Duration(i) * time.Hour),
			IngestedAt: baseTime.Add(time.Duration(i) * time.Hour),
			ValidFrom:  baseTime.Add(time.Duration(i) * time.Hour),
			CreatedAt:  baseTime.Add(time.Duration(i) * time.Hour),
		}
		if err := st.WriteMemoryV2(ctx, rec); err != nil {
			t.Fatalf("WriteMemoryV2 cand-%d: %v", i, err)
		}
	}

	// Also insert a durable record that should not appear in candidate queue
	durable := model.MemoryRecordV2{
		ID:         "durable-1",
		ProjectID:  "proj-queue",
		Kind:       model.MemoryKindDecision,
		Lifecycle:  model.MemoryDurable,
		Confidence: model.ConfidenceVerified,
		Authority:  model.AuthorityOperator,
		Title:      "Durable 1",
		Body:       "Body durable",
		Scope:      "project",
		ScopeID:    "proj-queue",
		ObservedAt: baseTime.Add(10 * time.Hour),
		IngestedAt: baseTime.Add(10 * time.Hour),
		ValidFrom:  baseTime.Add(10 * time.Hour),
		CreatedAt:  baseTime.Add(10 * time.Hour),
	}
	if err := st.WriteMemoryV2(ctx, durable); err != nil {
		t.Fatalf("WriteMemoryV2 durable: %v", err)
	}

	overview, err := st.MemoryOverview(ctx, "proj-queue", MemoryOverviewLimits{Queue: 3, Recalls: 10})
	if err != nil {
		t.Fatalf("MemoryOverview failed: %v", err)
	}

	if len(overview.ReviewQueue) != 3 {
		t.Fatalf("expected 3 queue items, got %d", len(overview.ReviewQueue))
	}

	// Newest first: cand-5, cand-4, cand-3
	expectedIDs := []string{"cand-5", "cand-4", "cand-3"}
	for i, exp := range expectedIDs {
		if overview.ReviewQueue[i].ID != exp {
			t.Errorf("queue[%d]: expected %s, got %s", i, exp, overview.ReviewQueue[i].ID)
		}
		if overview.ReviewQueue[i].Kind != model.MemoryKindDecision {
			t.Errorf("queue[%d]: expected KindDecision, got %s", i, overview.ReviewQueue[i].Kind)
		}
		if overview.ReviewQueue[i].Authority != model.AuthorityAgent {
			t.Errorf("queue[%d]: expected AuthorityAgent, got %s", i, overview.ReviewQueue[i].Authority)
		}
		if overview.ReviewQueue[i].CreatedAt == nil {
			t.Errorf("queue[%d]: expected non-nil CreatedAt", i)
		}
	}
}

func TestMemoryOverviewEditionTagParsing(t *testing.T) {
	st := setupTestStore(t, "proj-tags")
	ctx := t.Context()

	baseTime := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	titles := []string{
		"[extreme/routing] Route requests",
		"[extreme/routing] Secondary route",
		"[extreme/auth] Token verification",
		"[community/storage] Disk store",
		"[shared/net] Socket handler",
		"[shared/net] Proxy handler",
		"plain untagged record",
		"[unrecognized/prefix] Something else",
		"[extreme/] empty area",
	}

	for i, title := range titles {
		rec := model.MemoryRecordV2{
			ID:         fmt.Sprintf("tag-%d", i),
			ProjectID:  "proj-tags",
			Kind:       model.MemoryKindSemantic,
			Lifecycle:  model.MemoryDurable,
			Confidence: model.ConfidenceVerified,
			Authority:  model.AuthorityOperator,
			Title:      title,
			Body:       "Body",
			Scope:      "project",
			ScopeID:    "proj-tags",
			ObservedAt: baseTime,
			IngestedAt: baseTime,
			ValidFrom:  baseTime,
			CreatedAt:  baseTime,
		}
		if err := st.WriteMemoryV2(ctx, rec); err != nil {
			t.Fatalf("WriteMemoryV2 tag-%d: %v", i, err)
		}
	}

	overview, err := st.MemoryOverview(ctx, "proj-tags", MemoryOverviewLimits{Queue: 10, Recalls: 10})
	if err != nil {
		t.Fatalf("MemoryOverview failed: %v", err)
	}

	// 3 extreme ([extreme/routing], [extreme/routing], [extreme/auth]) + 1 with empty area [extreme/] = 4
	if overview.EditionTags.ExtremeCount != 4 {
		t.Errorf("expected 4 extreme records, got %d", overview.EditionTags.ExtremeCount)
	}
	if overview.EditionTags.CommunityCount != 1 {
		t.Errorf("expected 1 community record, got %d", overview.EditionTags.CommunityCount)
	}
	if overview.EditionTags.SharedCount != 2 {
		t.Errorf("expected 2 shared records, got %d", overview.EditionTags.SharedCount)
	}

	// Areas
	hasArea := func(areas []string, target string) bool {
		for _, a := range areas {
			if a == target {
				return true
			}
		}
		return false
	}

	if !hasArea(overview.EditionTags.ExtremeAreas, "routing") || !hasArea(overview.EditionTags.ExtremeAreas, "auth") {
		t.Errorf("extreme areas mismatch: %+v", overview.EditionTags.ExtremeAreas)
	}
	if len(overview.EditionTags.ExtremeAreas) != 2 {
		t.Errorf("expected 2 distinct extreme areas, got %d: %+v", len(overview.EditionTags.ExtremeAreas), overview.EditionTags.ExtremeAreas)
	}
	if !hasArea(overview.EditionTags.CommunityAreas, "storage") || len(overview.EditionTags.CommunityAreas) != 1 {
		t.Errorf("community areas mismatch: %+v", overview.EditionTags.CommunityAreas)
	}
	if !hasArea(overview.EditionTags.SharedAreas, "net") || len(overview.EditionTags.SharedAreas) != 1 {
		t.Errorf("shared areas mismatch: %+v", overview.EditionTags.SharedAreas)
	}
}

func TestMemoryOverviewRecallsLimitAndSnapshots(t *testing.T) {
	st := setupTestStore(t, "proj-recalls")
	ctx := t.Context()

	baseTime := time.Date(2026, 11, 2, 14, 0, 0, 0, time.UTC)
	for i := 1; i <= 5; i++ {
		rc := RetrievalReceiptRecord{
			ReceiptID:     fmt.Sprintf("rc-%d", i),
			ProjectID:     "proj-recalls",
			CallerID:      fmt.Sprintf("caller-%d", i),
			QueryDigest:   fmt.Sprintf("digest-%d", i),
			TaskID:        fmt.Sprintf("task-%d", i),
			OutcomeStatus: "success",
			DeniedCount:   0,
			CreatedAt:     baseTime.Add(time.Duration(i) * time.Minute),
		}
		if err := st.WriteRetrievalReceipt(ctx, rc); err != nil {
			t.Fatalf("WriteRetrievalReceipt rc-%d: %v", i, err)
		}
	}

	// Insert snapshot directly or via store API
	snapTime := baseTime.Add(10 * time.Minute)
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO memory_restore_snapshots(project_id, snapshot_id, manifest_digest, manifest_json, payload, created_at)
		VALUES(?, 'snap-1', 'digest-snap', '{"version":1}', x'0102', ?)
	`, "proj-recalls", snapTime.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert snapshot: %v", err)
	}

	// Insert task and mcp gateway logs
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO tasks(task_id, project_id, title, status, risk, revision, created_at, updated_at)
		VALUES('task-1', 'proj-recalls', 'test task', 'proposed', 'R1', 0, ?, ?)
	`, baseTime.Format(time.RFC3339Nano), baseTime.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert task: %v", err)
	}

	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO mcp_gateway_logs(log_id, subject, task_id, server, tool, allowed, created_at)
		VALUES
			('log-1', 'sub-1', 'task-1', 'srv-1', 'recall_tool', 1, ?),
			('log-2', 'sub-2', 'task-1', 'srv-1', 'recall_tool', 1, ?),
			('log-3', 'sub-3', 'task-1', 'srv-1', 'store_tool', 1, ?)
	`, baseTime.Format(time.RFC3339Nano), baseTime.Format(time.RFC3339Nano), baseTime.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert mcp log: %v", err)
	}

	overview, err := st.MemoryOverview(ctx, "proj-recalls", MemoryOverviewLimits{Queue: 10, Recalls: 2})
	if err != nil {
		t.Fatalf("MemoryOverview failed: %v", err)
	}

	if overview.RecentRecalls.TotalReceipts != 5 {
		t.Errorf("expected 5 total receipts, got %d", overview.RecentRecalls.TotalReceipts)
	}
	if len(overview.RecentRecalls.Recent) != 2 {
		t.Fatalf("expected 2 recent receipts, got %d", len(overview.RecentRecalls.Recent))
	}
	// Newest first: rc-5, rc-4
	if overview.RecentRecalls.Recent[0].ReceiptID != "rc-5" || overview.RecentRecalls.Recent[1].ReceiptID != "rc-4" {
		t.Errorf("recent recalls order mismatch: got %s, %s", overview.RecentRecalls.Recent[0].ReceiptID, overview.RecentRecalls.Recent[1].ReceiptID)
	}

	// Verify snapshots
	if overview.Snapshots.TotalSnapshots != 1 {
		t.Errorf("expected 1 snapshot, got %d", overview.Snapshots.TotalSnapshots)
	}
	if overview.Snapshots.NewestSnapshot == nil || !overview.Snapshots.NewestSnapshot.Equal(snapTime) {
		t.Errorf("expected newest snapshot %v, got %v", snapTime, overview.Snapshots.NewestSnapshot)
	}

	// Verify MCP gateway tool counts
	if overview.MCPTools.ToolCalls["recall_tool"] != 2 {
		t.Errorf("expected 2 recall_tool calls, got %d", overview.MCPTools.ToolCalls["recall_tool"])
	}
	if overview.MCPTools.ToolCalls["store_tool"] != 1 {
		t.Errorf("expected 1 store_tool calls, got %d", overview.MCPTools.ToolCalls["store_tool"])
	}
	if overview.MCPTools.CandidatesCount != 0 {
		t.Errorf("expected 0 candidates in community mode, got %d", overview.MCPTools.CandidatesCount)
	}
	if overview.MCPTools.ProposalsCount != 0 {
		t.Errorf("expected 0 proposals in community mode, got %d", overview.MCPTools.ProposalsCount)
	}
}
