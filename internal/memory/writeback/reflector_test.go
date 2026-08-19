package writeback_test

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/writeback"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT119PostRunReflectionAndWriteback(t *testing.T) {
	reflector := writeback.NewReflector()
	ctx := context.Background()
	now := time.Now().UTC()

	// 1. Successful verified run writeback
	successOutcome := writeback.RunOutcome{
		TaskID:          "TASK-SUCCESS-1",
		ProjectID:       "PROJ-1",
		Status:          "SUCCESS",
		CommitSHA:       "commit-sha-12345",
		VerificationIDs: []string{"VERIFY-PASS-1"},
		KeyDecisions:    []string{"Enable WAL mode for SQLite"},
		ProcedureNotes:  "Step 1: pragma journal_mode=wal. Step 2: pragma busy_timeout=5000.",
		ObservedAt:      now,
	}

	successCandidate, err := reflector.ReflectAndWriteback(ctx, successOutcome)
	if err != nil {
		t.Fatalf("ReflectAndWriteback success: %v", err)
	}

	if successCandidate.Kind != model.MemoryKindDecision && successCandidate.Kind != model.MemoryKindProcedural {
		t.Fatalf("expected decision or procedural kind, got: %s", successCandidate.Kind)
	}
	if successCandidate.HeadCommit != "commit-sha-12345" {
		t.Fatalf("expected HeadCommit commit-sha-12345, got: %s", successCandidate.HeadCommit)
	}
	if len(successCandidate.EvidenceIDs) == 0 || successCandidate.EvidenceIDs[0] != "VERIFY-PASS-1" {
		t.Fatalf("expected bound verification evidence ID, got: %+v", successCandidate.EvidenceIDs)
	}

	// 2. Failed run writeback: Must record failure finding without pretending success
	failedOutcome := writeback.RunOutcome{
		TaskID:          "TASK-FAIL-1",
		ProjectID:       "PROJ-1",
		Status:          "FAILED",
		CommitSHA:       "commit-fail-67890",
		ErrorMessage:    "sqlite3.OperationalError: database is locked",
		ObservedAt:      now,
	}

	failedCandidate, err := reflector.ReflectAndWriteback(ctx, failedOutcome)
	if err != nil {
		t.Fatalf("ReflectAndWriteback failure: %v", err)
	}

	if failedCandidate.Kind != model.MemoryKindFinding {
		t.Fatalf("expected finding kind for failure run, got: %s", failedCandidate.Kind)
	}
	if failedCandidate.Confidence != model.ConfidenceObserved {
		t.Fatalf("expected observed confidence on failure run, got: %s", failedCandidate.Confidence)
	}

	// 3. Cancelled run writeback
	cancelledOutcome := writeback.RunOutcome{
		TaskID:     "TASK-CANCEL-1",
		ProjectID:  "PROJ-1",
		Status:     "CANCELLED",
		ObservedAt: now,
	}
	cancelledCandidate, err := reflector.ReflectAndWriteback(ctx, cancelledOutcome)
	if err != nil {
		t.Fatalf("ReflectAndWriteback cancelled: %v", err)
	}
	if cancelledCandidate.ID == "" {
		t.Fatal("expected non-empty cancelled outcome candidate")
	}
}
