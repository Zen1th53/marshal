package procedure_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/procedure"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT120ProceduralMemoryDistillation(t *testing.T) {
	d := procedure.NewDistiller(procedure.Config{
		MinVerifiedSuccesses: 2,
	})
	ctx := context.Background()

	// 1. One success is insufficient to distill procedure
	singleRun := []procedure.WorkflowEvidence{
		{
			TaskID:      "TASK-1",
			WorkflowSig: "sqlite-migration-wal",
			Success:     true,
			Steps:       []string{"Set WAL mode", "Run PRAGMA synchronous=NORMAL"},
			CommitSHA:   "commit-1",
		},
	}
	_, err := d.DistillProcedure(ctx, "PROJ-1", "sqlite-migration-wal", singleRun)
	if !errors.Is(err, procedure.ErrInsufficientEvidence) {
		t.Fatalf("expected ErrInsufficientEvidence for 1 success, got: %v", err)
	}

	// 2. Repeated verified workflow (2 successes) proposes procedural candidate
	repeatedRuns := []procedure.WorkflowEvidence{
		{
			TaskID:      "TASK-1",
			WorkflowSig: "sqlite-migration-wal",
			Success:     true,
			Steps:       []string{"Set WAL mode", "Run PRAGMA synchronous=NORMAL"},
			CommitSHA:   "commit-1",
		},
		{
			TaskID:      "TASK-2",
			WorkflowSig: "sqlite-migration-wal",
			Success:     true,
			Steps:       []string{"Set WAL mode", "Run PRAGMA synchronous=NORMAL"},
			CommitSHA:   "commit-2",
		},
	}

	proc, err := d.DistillProcedure(ctx, "PROJ-1", "sqlite-migration-wal", repeatedRuns)
	if err != nil {
		t.Fatalf("DistillProcedure: %v", err)
	}

	if proc.Kind != model.MemoryKindProcedural {
		t.Fatalf("expected procedural memory kind, got: %s", proc.Kind)
	}
	if proc.Confidence != model.ConfidenceVerified {
		t.Fatalf("expected verified confidence for 2 clean successes, got: %s", proc.Confidence)
	}

	// 3. Contradictory failure lowers confidence
	mixedRuns := append(repeatedRuns, procedure.WorkflowEvidence{
		TaskID:      "TASK-3",
		WorkflowSig: "sqlite-migration-wal",
		Success:     false,
		ErrorReason: "Deadlock on busy_timeout=0",
		CommitSHA:   "commit-3",
	})

	procMixed, err := d.DistillProcedure(ctx, "PROJ-1", "sqlite-migration-wal", mixedRuns)
	if err != nil {
		t.Fatalf("DistillProcedure mixed: %v", err)
	}
	if procMixed.Confidence == model.ConfidenceVerified {
		t.Fatalf("expected lower confidence when failures are present, got: %s", procMixed.Confidence)
	}
}
