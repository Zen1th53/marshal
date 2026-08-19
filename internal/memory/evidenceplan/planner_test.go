package evidenceplan_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/evidenceplan"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT148PostRetrievalEvidencePlan(t *testing.T) {
	ctx := context.Background()
	planner := evidenceplan.NewPlanner()

	records := []model.MemoryRecordV2{
		{
			ID:         "MEM-FACT-1",
			Title:      "SQLite WAL Configuration",
			Body:       "PRAGMA journal_mode=WAL;",
			Kind:       model.MemoryKindDecision,
			Authority:  model.AuthorityOperator,
			Lifecycle:  model.MemoryDurable,
			Confidence: model.ConfidenceVerified,
		},
		{
			ID:         "MEM-SKILL-1",
			Title:      "Run DB Migration",
			Body:       "Run go test ./internal/store/... to verify migration",
			Kind:       model.MemoryKindProcedural,
			Authority:  model.AuthorityVerified,
			Lifecycle:  model.MemoryDurable,
			Confidence: model.ConfidenceVerified,
		},
		{
			ID:         "MEM-UNVERIFIED-1",
			Title:      "Unverified Cache Tweak",
			Body:       "PRAGMA cache_size = -64000;",
			Kind:       model.MemoryKindWorking,
			Authority:  model.AuthorityAgent,
			Lifecycle:  model.MemoryCandidate,
			Confidence: model.ConfidenceInferred,
		},
	}

	conflicts := []evidenceplan.ConflictItem{
		{
			RecordID:          "MEM-FACT-1",
			ConflictingWithID: "MEM-OLD-DELETE-MODE",
			Reason:            "Conflicting journal mode settings",
		},
	}

	plan, err := planner.BuildPlan(ctx, records, conflicts, 2048)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}

	// 1. Separation of verified facts, procedures, and unverified beliefs
	if len(plan.VerifiedFacts) != 1 || plan.VerifiedFacts[0].ID != "MEM-FACT-1" {
		t.Fatalf("expected 1 verified fact, got: %+v", plan.VerifiedFacts)
	}
	if len(plan.Procedures) != 1 || plan.Procedures[0].ID != "MEM-SKILL-1" {
		t.Fatalf("expected 1 procedure, got: %+v", plan.Procedures)
	}
	if len(plan.CandidateBeliefs) != 1 || plan.CandidateBeliefs[0].ID != "MEM-UNVERIFIED-1" {
		t.Fatalf("expected 1 candidate belief, got: %+v", plan.CandidateBeliefs)
	}

	// 2. Conflicts group
	if len(plan.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict group, got: %d", len(plan.Conflicts))
	}

	// 3. Required fresh checks for unverified candidate
	if len(plan.RequiredFreshChecks) == 0 {
		t.Fatal("expected fresh verification check generated for unverified candidate")
	}

	// 4. Render XML format
	rendered := plan.RenderXML()
	if !strings.Contains(rendered, "<grounded_evidence_plan") || !strings.Contains(rendered, "</grounded_evidence_plan>") {
		t.Fatalf("unexpected rendered XML output: %s", rendered)
	}
}
