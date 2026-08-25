package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestWorktreeMemoryIsLocalUntilCanonicalHeadAdvances(t *testing.T) {
	ctx := context.Background()
	rt, svc := openTestMemoryService(t)
	const taskID = "TASK-WORKTREE-FRESHNESS"
	principal := testPrincipal("AGENT-worktree-a")
	other := testPrincipal("AGENT-worktree-b")
	grantTaskMemoryAccess(t, rt, taskID, principal, other)

	now := time.Now().UTC()
	rec := model.MemoryRecordV2{
		ID: "MEM-WORKTREE-LOCAL", ProjectID: "PROJECT-local",
		Kind: model.MemoryKindFinding, Lifecycle: model.MemoryCandidate,
		Confidence: model.ConfidenceObserved, Authority: model.AuthorityAgent,
		Title: "worktree implementation", Body: "foo.go uses implementation X",
		Scope: string(model.ScopeTask), ScopeID: taskID,
		Source:     model.MemorySource{Kind: "runtime", Reference: "RUN-A", AgentID: principal.ID},
		HeadCommit: "worktree-a-head", BranchName: "marshal/task-a", WorktreeID: "WT-A",
		ObservedAt: now, IngestedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := rt.Store().WriteMemoryV2(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if err := svc.IndexRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}

	local := svc.EvaluateFreshness(rec, MemoryReconcileRequest{
		CurrentHead: "worktree-a-head", CanonicalHead: "main-head", CurrentWorktreeID: "WT-A",
	})
	if local.Classification != FreshnessWorktreeLocal {
		t.Fatalf("same worktree freshness = %+v", local)
	}

	foreign, err := svc.Recall(ctx, other, RecallRequest{
		ProjectID: "PROJECT-local", Query: "implementation X", AllowedScopeIDs: []string{taskID},
		CurrentHead: "worktree-b-head", CanonicalHead: "main-head", CurrentWorktreeID: "WT-B",
		MaxRecords: 4, MaxBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(foreign.Results) != 0 || strings.Contains(foreign.Context, rec.Title) {
		t.Fatalf("unmerged worktree fact leaked cross-worktree: %+v", foreign)
	}
	if len(foreign.Receipt.Decisions) != 1 || !strings.HasPrefix(foreign.Receipt.Decisions[0].Reason, "worktree_mismatch:") {
		t.Fatalf("missing safe worktree exclusion receipt: %+v", foreign.Receipt)
	}

	merged := svc.EvaluateFreshness(rec, MemoryReconcileRequest{
		CurrentHead: "worktree-a-head", CanonicalHead: "worktree-a-head", CurrentWorktreeID: "WT-B",
	})
	if merged.Classification != FreshnessFresh {
		t.Fatalf("merged worktree fact freshness = %+v", merged)
	}
}
