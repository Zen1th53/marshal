package app

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestM13_ReconciliationAndFreshnessGrading(t *testing.T) {
	ctx := context.Background()
	_, svc := openTestMemoryService(t)

	const projectID = "PROJECT-local"
	p := testPrincipal("developer-1")

	// 1. Invariant architectural decision (not anchored to a single transient file or commit)
	archDecision, err := svc.Remember(ctx, p, RememberRequest{
		ProjectID: projectID,
		Title:     "Zero CGO Invariant",
		Body:      "All production binaries must compile with CGO_ENABLED=0",
		Kind:      model.MemoryKindDecision,
		Scope:     model.ScopeProject,
	})
	if err != nil {
		t.Fatalf("remember arch decision: %v", err)
	}

	// 2. Memory grounded in a specific source file
	fileGroundedRec, err := svc.ExtractCandidate(ctx, p, ExtractCandidateRequest{
		ProjectID: projectID,
		Kind:      model.MemoryKindSemantic,
		Title:     "Old Legacy Auth Helper Function",
		Body:      "legacyAuth() in internal/legacy/auth.go parses bearer headers",
		Scope:     model.ScopeProject,
		ExtMeta: map[string]any{
			"file_path": "internal/legacy/auth.go",
		},
	})
	if err != nil {
		t.Fatalf("extract file grounded rec: %v", err)
	}

	// 3. Episodic run outcome anchored to old commit
	oldCommit := "commit-111111"
	newCommit := "commit-222222"
	episodicRec, err := svc.ExtractCandidate(ctx, p, ExtractCandidateRequest{
		ProjectID:  projectID,
		Kind:       model.MemoryKindEpisodic,
		Title:      "Run outcome on commit 111111",
		Body:       "Tests succeeded on commit 111111",
		Scope:      model.ScopeProject,
		HeadCommit: oldCommit,
		BranchName: "main",
	})
	if err != nil {
		t.Fatalf("extract episodic rec: %v", err)
	}

	// 4. Reconcile against advanced repository where legacy file was deleted
	reconcileReport, err := svc.Reconcile(ctx, p, MemoryReconcileRequest{
		ProjectID:     projectID,
		CurrentHead:   newCommit,
		CurrentBranch: "main",
		DeletedFiles:  []string{"internal/legacy/auth.go"},
	})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if reconcileReport.StaleCount < 2 {
		t.Fatalf("expected at least 2 stale records (deleted file + old episodic), got %+v", reconcileReport)
	}

	// 5. Evaluate recall when searching with new head commit and deleted file context
	recallRes, err := svc.Recall(ctx, p, RecallRequest{
		ProjectID:     projectID,
		Query:         "Zero CGO",
		CurrentHead:   newCommit,
		CurrentBranch: "main",
	})
	if err != nil {
		t.Fatalf("recall arch decision: %v", err)
	}
	if len(recallRes.Results) == 0 || recallRes.Results[0].ID != archDecision.ID {
		t.Fatalf("expected invariant architectural decision to remain recalled across commit advance: %+v", recallRes)
	}

	// 6. Searching for deleted file memory must not return it as active ground truth
	deletedRecallRes, err := svc.Recall(ctx, p, RecallRequest{
		ProjectID:     projectID,
		Query:         "legacyAuth",
		CurrentHead:   newCommit,
		CurrentBranch: "main",
		DeletedFiles:  []string{"internal/legacy/auth.go"},
	})
	if err != nil {
		t.Fatalf("recall deleted file query: %v", err)
	}
	for _, it := range deletedRecallRes.Results {
		if it.ID == fileGroundedRec.ID {
			t.Fatalf("deleted file memory returned as active ground truth: %+v", deletedRecallRes)
		}
	}
	_ = episodicRec
}

func TestM13_BranchDivergenceRankPenalty(t *testing.T) {
	ctx := context.Background()
	_, svc := openTestMemoryService(t)

	const projectID = "PROJECT-local"
	p := testPrincipal("developer-1")

	// Memory anchored to feature branch
	_, err := svc.ExtractCandidate(ctx, p, ExtractCandidateRequest{
		ProjectID:  projectID,
		Kind:       model.MemoryKindSemantic,
		Title:      "Experimental Caching Configuration",
		Body:       "Config cache size set to 1024MB on feature branch",
		Scope:      model.ScopeProject,
		BranchName: "feat/cache-experiment",
		HeadCommit: "head-feat-123",
	})
	if err != nil {
		t.Fatalf("extract feat branch memory: %v", err)
	}

	// Recall on main branch
	res, err := svc.Recall(ctx, p, RecallRequest{
		ProjectID:     projectID,
		Query:         "Experimental Caching",
		CurrentBranch: "main",
		CurrentHead:   "head-main-456",
	})
	if err != nil {
		t.Fatalf("recall on main: %v", err)
	}
	if len(res.Receipt.Decisions) > 0 {
		reason := res.Receipt.Decisions[0].Reason
		if reason == "" {
			t.Fatalf("expected staleness/divergence reason in receipt decision: %+v", res.Receipt.Decisions)
		}
	}
}

func TestM13_ValidityIntervalExpiration(t *testing.T) {
	ctx := context.Background()
	rt, svc := openTestMemoryService(t)

	const projectID = "PROJECT-local"
	p := testPrincipal("developer-1")

	past := time.Now().UTC().Add(-1 * time.Hour)
	expiredRec := model.MemoryRecordV2{
		ID:         "MEM-EXPIRED-1",
		ProjectID:  projectID,
		Kind:       model.MemoryKindSemantic,
		Lifecycle:  model.MemoryDurable,
		Confidence: model.ConfidenceVerified,
		Authority:  model.AuthorityOperator,
		Title:      "Temporary Maintenance Window",
		Body:       "Maintenance window active from past hour",
		Scope:      string(model.ScopeProject),
		ScopeID:    projectID,
		ObservedAt: past,
		IngestedAt: past,
		ValidFrom:  past,
		ValidTo:    &past,
		CreatedAt:  past,
		UpdatedAt:  past,
		Source:     model.MemorySource{Kind: "operator", Reference: "operator"},
	}
	if err := rt.Store().WriteMemoryV2(ctx, expiredRec); err != nil {
		t.Fatalf("write expired: %v", err)
	}
	svc.IndexRecord(ctx, expiredRec)

	res, err := svc.Recall(ctx, p, RecallRequest{
		ProjectID: projectID,
		Query:     "Maintenance Window",
	})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	for _, it := range res.Results {
		if it.ID == expiredRec.ID {
			t.Fatalf("expired record returned in recall results: %+v", res)
		}
	}
	if len(res.Receipt.Decisions) == 0 || res.Receipt.Decisions[0].Reason != "expired: valid_to expiration interval has passed" {
		t.Fatalf("expected expired reason in receipt decision: %+v", res.Receipt.Decisions)
	}
}

func TestM13_RepositoryEvidenceSignals(t *testing.T) {
	rec := model.MemoryRecordV2{
		ID: "MEM-CODE-SIGNALS", ProjectID: "PROJECT-local", Kind: model.MemoryKindSemantic,
		Lifecycle: model.MemoryCandidate, HeadCommit: "old-head", BranchName: "main",
		ExtMeta: map[string]any{
			"referenced_files": []any{"internal/app/old.go"},
			"file_hashes":      map[string]any{"internal/app/old.go": "old-hash"},
			"symbols":          []any{"app.oldSymbol"},
			"verified_tests":   []any{"TestOldBehavior"},
		},
	}
	_, svc := openTestMemoryService(t)
	tests := []struct {
		name string
		req  MemoryReconcileRequest
		want FreshnessClassification
	}{
		{"rename", MemoryReconcileRequest{RenamedFiles: map[string]string{"internal/app/old.go": "internal/app/new.go"}}, FreshnessPossiblyStale},
		{"hash", MemoryReconcileRequest{CurrentFileHashes: map[string]string{"internal/app/old.go": "new-hash"}}, FreshnessStale},
		{"symbol", MemoryReconcileRequest{ExistingSymbols: map[string]bool{"app.oldSymbol": false}}, FreshnessStale},
		{"test", MemoryReconcileRequest{InvalidatedTests: []string{"TestOldBehavior"}}, FreshnessPossiblyStale},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := svc.EvaluateFreshness(rec, tc.req)
			if got.Classification != tc.want || got.Reason == "" {
				t.Fatalf("EvaluateFreshness() = %+v, want %s with reason", got, tc.want)
			}
		})
	}
}
