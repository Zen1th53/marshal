package extract_test

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/extract"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT85ExtractFromHandoff(t *testing.T) {
	pipe := extract.NewPipeline()
	ctx := context.Background()

	handoffInput := extract.HandoffInput{
		ProjectID:   "PROJ-T85",
		TaskID:      "TASK-100",
		FromAgentID: "agent-a",
		ToAgentID:   "agent-b",
		Summary:     "Completed database migration v68, handing off for QA",
		EvidenceIDs: []string{"EVID-H1"},
		SessionID:   "SESS-01",
		RunID:       "RUN-01",
		HeadCommit:  "commit-abc1234",
	}

	cand, err := pipe.ExtractFromHandoff(ctx, handoffInput)
	if err != nil {
		t.Fatalf("ExtractFromHandoff: %v", err)
	}

	if cand.Kind != model.MemoryKindHandoff {
		t.Fatalf("expected kind %s, got %s", model.MemoryKindHandoff, cand.Kind)
	}
	if cand.Lifecycle != model.MemoryCandidate {
		t.Fatalf("extracted memory must be Candidate, got %s", cand.Lifecycle)
	}
	if cand.Source.AgentID != "agent-a" {
		t.Fatalf("expected source agent agent-a, got %s", cand.Source.AgentID)
	}
	if cand.HeadCommit != "commit-abc1234" {
		t.Fatalf("expected head commit commit-abc1234, got %s", cand.HeadCommit)
	}
	if len(cand.EvidenceIDs) != 1 || cand.EvidenceIDs[0] != "EVID-H1" {
		t.Fatalf("expected evidence EVID-H1, got %+v", cand.EvidenceIDs)
	}
}

func TestT85ExtractFromRunOutcome(t *testing.T) {
	pipe := extract.NewPipeline()
	ctx := context.Background()

	// Successful run extraction
	successInput := extract.RunOutcomeInput{
		ProjectID:   "PROJ-T85",
		TaskID:      "TASK-200",
		AgentID:     "agent-dev",
		SessionID:   "SESS-02",
		RunID:       "RUN-02",
		Success:     true,
		Title:       "Implemented Task 200",
		Summary:     "Refactored scheduler with multi-factor scoring",
		HeadCommit:  "commit-def5678",
		EvidenceIDs: []string{"EVID-R1"},
	}

	cand, err := pipe.ExtractFromRun(ctx, successInput)
	if err != nil {
		t.Fatalf("ExtractFromRun success: %v", err)
	}
	if cand.Kind != model.MemoryKindSemantic {
		t.Fatalf("expected semantic kind, got %s", cand.Kind)
	}
	if cand.Lifecycle != model.MemoryCandidate {
		t.Fatalf("expected candidate lifecycle, got %s", cand.Lifecycle)
	}

	// Failed/cancelled run extraction -> extracts Failure memory candidate, never false success
	failInput := extract.RunOutcomeInput{
		ProjectID:   "PROJ-T85",
		TaskID:      "TASK-200",
		AgentID:     "agent-dev",
		SessionID:   "SESS-02",
		RunID:       "RUN-03",
		Success:     false,
		Title:       "Run Failed",
		Summary:     "Build timeout on integration tests",
		HeadCommit:  "commit-def5678",
		EvidenceIDs: []string{"EVID-F1"},
	}

	failCand, err := pipe.ExtractFromRun(ctx, failInput)
	if err != nil {
		t.Fatalf("ExtractFromRun failure: %v", err)
	}
	if failCand.Kind != model.MemoryKindFailure {
		t.Fatalf("failed run must produce Failure kind, got %s", failCand.Kind)
	}
	if failCand.Lifecycle != model.MemoryCandidate {
		t.Fatalf("expected candidate lifecycle, got %s", failCand.Lifecycle)
	}
}

func TestT85MalformedInputRejected(t *testing.T) {
	pipe := extract.NewPipeline()
	ctx := context.Background()

	// Empty project ID or task ID must be rejected
	_, err := pipe.ExtractFromHandoff(ctx, extract.HandoffInput{
		ProjectID: "",
		TaskID:    "TASK-1",
	})
	if err == nil {
		t.Fatal("expected error for empty ProjectID")
	}

	_, err = pipe.ExtractFromRun(ctx, extract.RunOutcomeInput{
		ProjectID: "PROJ-1",
		TaskID:    "",
	})
	if err == nil {
		t.Fatal("expected error for empty TaskID")
	}
}
