package working_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/working"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT138WorkingMemoryPromotionBridge(t *testing.T) {
	ctx := context.Background()
	bridge := working.NewPromotionBridge()

	// 1. Failed hypothesis promotion is strictly DENIED
	failedSlot := working.WorkingSlot{
		Type:  working.SlotHypothesis,
		Value: "FALSIFIED: SQLite locks whole database on WAL read (Refuted by official docs)",
	}
	_, err := bridge.GraduateSlot(ctx, "PROJ-1", "TASK-1", "AGENT-A", failedSlot, []string{"EVID-1"}, model.MemoryKindDecision)
	if !errors.Is(err, working.ErrFailedHypothesisCannotPromote) {
		t.Fatalf("expected ErrFailedHypothesisCannotPromote for refuted hypothesis, got: %v", err)
	}

	// 2. Successful verified observation graduation into Candidate
	validSlot := working.WorkingSlot{
		Type:  working.SlotTemporaryObservations,
		Value: "Confirmed: SQLite journal_mode=WAL requires busy_timeout pragma to prevent busy handler spikes",
	}
	candidate, err := bridge.GraduateSlot(ctx, "PROJ-1", "TASK-1", "AGENT-A", validSlot, []string{"EVID-101"}, model.MemoryKindDecision)
	if err != nil {
		t.Fatalf("GraduateSlot: %v", err)
	}
	if candidate.Lifecycle != model.MemoryCandidate {
		t.Fatalf("expected candidate lifecycle on graduation, got: %s", candidate.Lifecycle)
	}
	if candidate.Source.Reference != "TASK-1" || len(candidate.EvidenceIDs) != 1 {
		t.Fatalf("expected source task and evidence preserved, got: %+v", candidate)
	}
}
