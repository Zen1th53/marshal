package store

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Zen1th53/marshal/internal/cell"
)

type concurrentCellBackend struct{ prepares atomic.Int32 }

func (b *concurrentCellBackend) Prepare(_ context.Context, spec cell.Spec) (cell.Handle, error) {
	b.prepares.Add(1)
	return cell.Handle{ID: "cell-concurrent", TaskID: spec.TaskID, Backend: spec.Backend, Workspace: spec.Workspace}, nil
}
func (b *concurrentCellBackend) Exec(context.Context, cell.Handle, cell.ExecRequest) (cell.ExecResult, error) {
	return cell.ExecResult{}, nil
}
func (b *concurrentCellBackend) Destroy(context.Context, cell.Handle) error { return nil }

type concurrentCellAuthorizer struct{}

func (concurrentCellAuthorizer) AuthorizeCellPrepare(context.Context, cell.Spec) error { return nil }

func TestA08TwoStoresOwnOneCellPreparation(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "cells.db")
	first, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, dbPath)
	if err != nil {
		first.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close(); _ = second.Close() })
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	backend := &concurrentCellBackend{}
	managers := []*cell.Manager{
		cell.NewManager(first, map[cell.BackendKind]cell.Backend{cell.BackendNative: backend}, concurrentCellAuthorizer{}),
		cell.NewManager(second, map[cell.BackendKind]cell.Backend{cell.BackendNative: backend}, concurrentCellAuthorizer{}),
	}
	spec := cell.Spec{TaskID: "TASK-cell-a08", Workspace: "/tmp/cell-a08", Backend: cell.BackendNative}
	start := make(chan struct{})
	var wg sync.WaitGroup
	const workers = 32
	results := make([]cell.Record, workers)
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], errs[i] = managers[i%len(managers)].Prepare(ctx, spec)
		}(i)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d Prepare: %v", i, err)
		}
		if results[i].State != cell.StateReady {
			t.Fatalf("worker %d state=%s, want ready", i, results[i].State)
		}
	}
	if got := backend.prepares.Load(); got != 1 {
		t.Fatalf("backend prepare calls=%d, want one durable owner", got)
	}
	if got := queryInt(t, first.db, "SELECT count(*) FROM execution_cells"); got != 1 {
		t.Fatalf("execution cell rows=%d, want one", got)
	}
}

func TestA08ReopenRetryReconcilesReadyCellWithoutBackendReplay(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "cells-reopen.db")
	backend := &concurrentCellBackend{}
	first, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Migrate(ctx); err != nil {
		first.Close()
		t.Fatal(err)
	}
	spec := cell.Spec{TaskID: "TASK-cell-reopen", Workspace: "/tmp/cell-reopen", Backend: cell.BackendNative}
	manager := cell.NewManager(first, map[cell.BackendKind]cell.Backend{cell.BackendNative: backend}, concurrentCellAuthorizer{})
	if _, err := manager.Prepare(ctx, spec); err != nil {
		first.Close()
		t.Fatal(err)
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
	manager = cell.NewManager(second, map[cell.BackendKind]cell.Backend{cell.BackendNative: backend}, concurrentCellAuthorizer{})
	result, err := manager.Prepare(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != cell.StateReady || backend.prepares.Load() != 1 {
		t.Fatalf("result=%+v backend prepare calls=%d, want ready/1", result, backend.prepares.Load())
	}
}
