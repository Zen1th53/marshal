package app

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestM17_OutcomeUtilityFeedbackAndAuthorityDominance(t *testing.T) {
	ctx := context.Background()
	rt, svc := openTestMemoryService(t)

	const projectID = "PROJECT-local"
	p := testPrincipal("developer-1")

	now := time.Now().UTC()

	// 1. Agent candidate memory A (will receive 10 repeated successes)
	agentA := model.MemoryRecordV2{
		ID:         "MEM-AGENT-A",
		ProjectID:  projectID,
		Kind:       model.MemoryKindSemantic,
		Lifecycle:  model.MemoryCandidate,
		Confidence: model.ConfidenceInferred,
		Authority:  model.AuthorityAgent,
		Title:      "Go build optimization helper",
		Body:       "Use go test -race to catch race conditions",
		Scope:      string(model.ScopeProject),
		ScopeID:    projectID,
		ObservedAt: now,
		IngestedAt: now,
		ValidFrom:  now,
		CreatedAt:  now,
		UpdatedAt:  now,
		Source:     model.MemorySource{Kind: "agent", Reference: "agent-1"},
	}
	if err := rt.Store().WriteMemoryV2(ctx, agentA); err != nil {
		t.Fatalf("write agent A: %v", err)
	}
	svc.IndexRecord(ctx, agentA)

	// 2. Agent candidate memory B (will receive repeated failures)
	agentB := model.MemoryRecordV2{
		ID:         "MEM-AGENT-B",
		ProjectID:  projectID,
		Kind:       model.MemoryKindSemantic,
		Lifecycle:  model.MemoryCandidate,
		Confidence: model.ConfidenceInferred,
		Authority:  model.AuthorityAgent,
		Title:      "Go build optimization bypass",
		Body:       "Use go test -v without race detector",
		Scope:      string(model.ScopeProject),
		ScopeID:    projectID,
		ObservedAt: now,
		IngestedAt: now,
		ValidFrom:  now,
		CreatedAt:  now,
		UpdatedAt:  now,
		Source:     model.MemorySource{Kind: "agent", Reference: "agent-2"},
	}
	if err := rt.Store().WriteMemoryV2(ctx, agentB); err != nil {
		t.Fatalf("write agent B: %v", err)
	}
	svc.IndexRecord(ctx, agentB)

	// 3. Operator durable memory (0 usage, cold start)
	opRec := model.MemoryRecordV2{
		ID:         "MEM-OPERATOR-POLICY",
		ProjectID:  projectID,
		Kind:       model.MemoryKindDecision,
		Lifecycle:  model.MemoryDurable,
		Confidence: model.ConfidenceVerified,
		Authority:  model.AuthorityOperator,
		Title:      "Go build production standard",
		Body:       "Standard go test commands must run in unprivileged container",
		Scope:      string(model.ScopeProject),
		ScopeID:    projectID,
		ObservedAt: now,
		IngestedAt: now,
		ValidFrom:  now,
		CreatedAt:  now,
		UpdatedAt:  now,
		Source:     model.MemorySource{Kind: "operator", Reference: "operator"},
	}
	if err := rt.Store().WriteMemoryV2(ctx, opRec); err != nil {
		t.Fatalf("write operator rec: %v", err)
	}
	svc.IndexRecord(ctx, opRec)

	// 4. Record 10 successes for Agent A
	for i := 0; i < 10; i++ {
		svc.RecordOutcome(ctx, agentA.ID, "TASK-1", true, false)
	}
	// Record 5 failures for Agent B
	for i := 0; i < 5; i++ {
		svc.RecordOutcome(ctx, agentB.ID, "TASK-2", false, false)
	}

	// Verify utility scores
	scoreA := svc.GetUtilityScore(ctx, agentA.ID)
	scoreB := svc.GetUtilityScore(ctx, agentB.ID)
	if scoreA <= scoreB {
		t.Fatalf("expected scoreA (%f) > scoreB (%f)", scoreA, scoreB)
	}
	persistedA, err := rt.Store().GetMemoryV2(ctx, projectID, agentA.ID)
	if err != nil || utilityScoreFromRecord(persistedA) != scoreA {
		t.Fatalf("utility was not persisted canonically: score=%f record=%+v err=%v", scoreA, persistedA.ExtMeta, err)
	}

	// 5. Perform Recall with query matching all three
	res, err := svc.Recall(ctx, p, RecallRequest{
		ProjectID: projectID,
		Query:     "go test",
	})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}

	if len(res.Results) < 3 {
		t.Fatalf("expected 3 results, got %d", len(res.Results))
	}

	// Invariant 1: Operator memory MUST rank first (#1) due to authority dominance
	if res.Results[0].ID != opRec.ID {
		t.Fatalf("expected operator record to dominate ranking regardless of popularity, got %s at rank 1", res.Results[0].ID)
	}

	// Invariant 2: Agent A (high utility) MUST rank ahead of Agent B (low utility)
	if res.Results[1].ID != agentA.ID || res.Results[2].ID != agentB.ID {
		t.Fatalf("expected Agent A (rank 2) ahead of Agent B (rank 3), got order: %+v", res.Results)
	}
}

func TestM17_UtilitySignalsAreIdempotentBoundedAndInactiveSafe(t *testing.T) {
	ctx := context.Background()
	rt, svc := openTestMemoryService(t)
	now := time.Now().UTC()
	rec := model.MemoryRecordV2{
		ID: "MEM-UTILITY-SIGNALS", ProjectID: "PROJECT-local", Kind: model.MemoryKindSemantic,
		Lifecycle: model.MemoryCandidate, Confidence: model.ConfidenceObserved, Authority: model.AuthorityAgent,
		Title: "utility", Body: "bounded utility signals", Scope: string(model.ScopeProject), ScopeID: "PROJECT-local",
		ObservedAt: now, IngestedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := rt.Store().WriteMemoryV2(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordUtilitySignal(ctx, rec.ID, "TASK-1", "event-1", UtilityHelpful, false); err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordUtilitySignal(ctx, rec.ID, "TASK-1", "event-1", UtilityHelpful, false); err != nil {
		t.Fatal(err)
	}
	persisted, err := rt.Store().GetMemoryV2(ctx, rec.ProjectID, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if count, _ := numericMeta(persisted.ExtMeta["utility_helpful_count"]); count != 1 {
		t.Fatalf("duplicate event changed count: %v", count)
	}
	for i := 0; i < 300; i++ {
		if err := svc.RecordUtilitySignal(ctx, rec.ID, "TASK-1", fmt.Sprintf("event-%d", i+2), UtilityUsed, false); err != nil {
			t.Fatal(err)
		}
	}
	persisted, err = rt.Store().GetMemoryV2(ctx, rec.ProjectID, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(stringMetaSlice(persisted.ExtMeta["utility_event_ids"])); got != 256 {
		t.Fatalf("event window = %d, want 256", got)
	}
	if _, err := rt.Store().TombstoneMemory(ctx, rec.ProjectID, rec.ID, persisted.Revision, "test"); err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordUtilitySignal(ctx, rec.ID, "TASK-1", "after-delete", UtilityUsed, false); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("inactive signal error = %v, want conflict", err)
	}
}
