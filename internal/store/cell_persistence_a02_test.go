package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/cell"
)

func TestA02ExecutionCellPersistenceMigratesAndReopens(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "cells.db")
	first, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Migrate(ctx); err != nil {
		first.Close()
		t.Fatal(err)
	}
	if got := queryInt(t, first.db, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='execution_cells'"); got != 1 {
		t.Fatalf("execution_cells tables = %d, want 1", got)
	}
	for _, index := range []string{"execution_cells_by_task", "execution_cells_by_state"} {
		if got := queryInt(t, first.db, "SELECT count(*) FROM sqlite_master WHERE type='index' AND name=?", index); got != 1 {
			t.Fatalf("index %s count = %d, want 1", index, got)
		}
	}
	now := time.Unix(1700000000, 0).UTC()
	record := cell.Record{
		ID:            "cell-a02",
		TaskID:        "TASK-cell-a02",
		Backend:       cell.BackendNative,
		Workspace:     "/tmp/cell-a02",
		SpecDigest:    "sha256:cell-a02",
		State:         cell.StatePreparing,
		ProcessRef:    "process-a02",
		FailureReason: "",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := first.PutCell(ctx, record); err != nil {
		first.Close()
		t.Fatalf("PutCell: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	if err := second.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := second.GetCell(ctx, record.ID)
	if err != nil {
		t.Fatalf("GetCell: %v", err)
	}
	if got != record {
		t.Fatalf("reopened record = %+v, want %+v", got, record)
	}
	if err := second.PutCell(ctx, record); err != nil {
		t.Fatalf("identical retry: %v", err)
	}
}
