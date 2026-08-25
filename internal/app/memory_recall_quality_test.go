package app

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/eval"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestMemoryRecallGoldenQuality(t *testing.T) {
	corpusFile, err := os.Open("../memory/eval/testdata/golden_relevance_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	defer corpusFile.Close()
	corpus, err := eval.LoadCorpus(corpusFile)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	rt, svc := openTestMemoryService(t)
	principal := testPrincipal("quality-reader")
	grantTaskMemoryAccess(t, rt, "TASK-T42", principal)
	now := time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC)
	for _, fixture := range corpus.Records {
		record := goldenCanonicalRecord(corpus.ProjectID, fixture, now)
		if err := rt.Store().WriteMemoryV2(ctx, record); err != nil {
			t.Fatalf("seed canonical record %s: %v", fixture.ID, err)
		}
	}
	if err := svc.RebuildProjections(ctx, corpus.ProjectID); err != nil {
		t.Fatalf("rebuild derived projections: %v", err)
	}

	outcomes := make([]eval.QueryOutcome, 0, len(corpus.Queries))
	for _, query := range corpus.Queries {
		request := goldenRecallRequest(corpus.ProjectID, query)
		started := time.Now()
		response, err := svc.Recall(ctx, principal, request)
		elapsed := time.Since(started)
		if err != nil {
			t.Fatalf("recall %s: %v", query.ID, err)
		}
		rankedIDs := recallItemIDs(response.Results)
		// A warm repeat proves ranking remains deterministic when the bounded
		// cache participates as a candidate-ID signal.
		warm, err := svc.Recall(ctx, principal, request)
		if err != nil {
			t.Fatalf("warm recall %s: %v", query.ID, err)
		}
		if warmIDs := recallItemIDs(warm.Results); !reflect.DeepEqual(rankedIDs, warmIDs) {
			t.Fatalf("non-deterministic ranking for %s: cold=%v warm=%v", query.ID, rankedIDs, warmIDs)
		}
		t.Logf("query=%s category=%s required=%v ranked=%v", query.ID, query.Category, query.Required, rankedIDs)

		for _, forbiddenID := range query.Forbidden["unauthorized"] {
			if containsString(rankedIDs, forbiddenID) || strings.Contains(response.Context, forbiddenID) {
				t.Fatalf("unauthorized record %s leaked in query %s", forbiddenID, query.ID)
			}
			for _, decision := range response.Receipt.Decisions {
				if decision.MemoryID == forbiddenID {
					t.Fatalf("unauthorized record %s leaked through receipt for %s", forbiddenID, query.ID)
				}
			}
		}
		outcomes = append(outcomes, eval.QueryOutcome{
			QueryID: query.ID, RankedIDs: rankedIDs,
			ContextBytes: len(response.Context), RecallDuration: elapsed,
		})
	}

	metrics, err := eval.Evaluate(corpus, outcomes, eval.DefaultCutoffs)
	if err != nil {
		t.Fatal(err)
	}
	for _, reason := range []string{"stale", "unauthorized", "tombstoned", "superseded", "conflicted"} {
		value, measured := metrics.ForbiddenExposureRate[reason]
		if !measured {
			t.Fatalf("golden corpus does not measure forbidden class %q", reason)
		}
		if value != 0 {
			t.Fatalf("forbidden %s exposure rate = %.4f, want zero", reason, value)
		}
	}
	if metrics.RecallAtK[10] == 0 || metrics.MRR == 0 || metrics.ContextBytesPerUsefulResult == 0 {
		t.Fatalf("quality evaluation produced no useful recall: %+v", metrics)
	}
	t.Logf("dataset_records=%d queries=%d", len(corpus.Records), len(corpus.Queries))
	for _, k := range eval.DefaultCutoffs {
		t.Logf("Recall@%d=%.4f Precision@%d=%.4f NDCG@%d=%.4f", k, metrics.RecallAtK[k], k, metrics.PrecisionAtK[k], k, metrics.NDCGAtK[k])
	}
	t.Logf("MRR=%.4f false_positive_recall_rate=%.4f forbidden=%v context_bytes_per_useful=%.2f estimated_context_tokens_per_useful=%.0f mean_time_to_first_useful_upper_bound=%s",
		metrics.MRR, metrics.FalsePositiveRecallRate, metrics.ForbiddenExposureRate,
		metrics.ContextBytesPerUsefulResult, metrics.ContextTokensPerUseful, metrics.MeanTimeToFirstUseful)
}

func goldenCanonicalRecord(projectID string, fixture eval.RecordFixture, now time.Time) model.MemoryRecordV2 {
	return model.MemoryRecordV2{
		ID: fixture.ID, ProjectID: projectID,
		Kind: model.MemoryKind(fixture.Kind), Lifecycle: model.MemoryLifecycle(fixture.Lifecycle),
		Confidence: model.MemoryConfidence(fixture.Confidence), Authority: model.MemoryAuthority(fixture.Authority),
		Title: fixture.Title, Body: fixture.Body,
		Scope: fixture.Scope, ScopeID: fixture.ScopeID, ACLScope: fixture.ACLScope,
		Source:      model.MemorySource{Kind: "test", Reference: "golden_relevance_v1"},
		EvidenceIDs: fixture.EvidenceIDs, HeadCommit: fixture.HeadCommit, BranchName: fixture.Branch,
		WorktreeID: fixture.WorktreeID, SupersededBy: fixture.SupersededBy,
		SupersedesID: fixture.Supersedes, ConflictIDs: fixture.ConflictIDs,
		ObservedAt: now, IngestedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now,
		ExtMeta: fixture.ExtMeta,
	}
}

func goldenRecallRequest(projectID string, query eval.QueryFixture) RecallRequest {
	return RecallRequest{
		ProjectID: projectID, Query: query.Text, AllowedScopeIDs: query.AllowedScopeIDs,
		CurrentHead: query.CurrentHead, CanonicalHead: query.CanonicalHead,
		CurrentBranch: query.CurrentBranch, CurrentWorktreeID: query.CurrentWorktreeID,
		ModifiedFiles: query.ModifiedFiles, DeletedFiles: query.DeletedFiles,
		RenamedFiles: query.RenamedFiles, CurrentFileHashes: query.CurrentFileHashes,
		ExistingSymbols: query.ExistingSymbols, InvalidatedTests: query.InvalidatedTests,
		MaxRecords: 10, MaxBytes: 64 << 10, RunID: "RUN-QUALITY-" + query.ID,
		TaskID: "TASK-QUALITY", Provider: "deterministic-runtime-evaluator",
	}
}

func recallItemIDs(items []RecallItem) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
