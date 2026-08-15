package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Zen1th53/marshal/internal/dag"
	"github.com/Zen1th53/marshal/internal/model"
)

func openTwoDAGStores(t *testing.T) (*Store, *Store) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "dag-a08.db")
	a, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open(ctx, path)
	if err != nil {
		a.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	if err := a.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return a, b
}

func TestT29A08ConcurrentOppositeEdgesHaveOneCanonicalWinner(t *testing.T) {
	ctx := context.Background()
	a, b := openTwoDAGStores(t)
	for _, id := range []dag.TaskID{"TASK-A", "TASK-B"} {
		if _, err := a.PutDAGNode(ctx, dag.Node{TaskID: id, Kind: dag.NodeKindTask, Status: dag.StatusPending}); err != nil {
			t.Fatal(err)
		}
	}
	edges := []dag.Edge{{From: "TASK-A", To: "TASK-B", Condition: dag.ConditionCompleted}, {From: "TASK-B", To: "TASK-A", Condition: dag.ConditionCompleted}}
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i, st := range []*Store{a, b} {
		i, st := i, st
		wg.Add(1)
		go func() { defer wg.Done(); _, err := st.PutDAGEdge(ctx, edges[i]); errs <- err }()
	}
	wg.Wait()
	close(errs)
	success, cycle := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			success++
		case errors.Is(err, dag.ErrCycle):
			cycle++
		default:
			t.Fatalf("unexpected concurrent edge error: %v", err)
		}
	}
	if success != 1 || cycle != 1 {
		t.Fatalf("success=%d cycle=%d", success, cycle)
	}
	var count int
	if err := a.db.QueryRowContext(ctx, "SELECT count(*) FROM dag_edges").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("edges=%d want=1", count)
	}
}

func TestT29A08ConflictingTerminalTransitionsHaveOneWinnerNoBusyLeak(t *testing.T) {
	ctx := context.Background()
	a, b := openTwoDAGStores(t)
	if _, err := a.PutDAGNode(ctx, dag.Node{TaskID: "TASK-X", Kind: dag.NodeKindTask, Status: dag.StatusPending}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.TransitionDAGNode(ctx, "TASK-X", dag.StatusPending, dag.StatusReady); err != nil {
		t.Fatal(err)
	}
	if _, err := a.TransitionDAGNode(ctx, "TASK-X", dag.StatusReady, dag.StatusRunning); err != nil {
		t.Fatal(err)
	}
	targets := []dag.NodeStatus{dag.StatusCompleted, dag.StatusFailed}
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i, st := range []*Store{a, b} {
		i, st := i, st
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := st.TransitionDAGNode(ctx, "TASK-X", dag.StatusRunning, targets[i])
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	success, conflict := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			success++
		case errors.Is(err, model.ErrConflict):
			conflict++
		default:
			t.Fatalf("unexpected transition error: %v", err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflict=%d", success, conflict)
	}
	var revision int
	if err := a.db.QueryRowContext(ctx, "SELECT revision FROM dag_nodes WHERE task_id='TASK-X'").Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if revision != 3 {
		t.Fatalf("revision=%d want=3", revision)
	}
}

func TestT29A08ExactTransitionRetryAcrossReopenDoesNotAdvanceRevision(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "dag-reopen-a08.db")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := first.PutDAGNode(ctx, dag.Node{TaskID: "TASK-R", Kind: dag.NodeKindTask, Status: dag.StatusPending}); err != nil {
		t.Fatal(err)
	}
	if _, err := first.TransitionDAGNode(ctx, "TASK-R", dag.StatusPending, dag.StatusReady); err != nil {
		t.Fatal(err)
	}
	_ = first.Close()
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := second.TransitionDAGNode(ctx, "TASK-R", dag.StatusPending, dag.StatusReady); err != nil {
		t.Fatal(err)
	}
	var revision int
	if err := second.db.QueryRowContext(ctx, "SELECT revision FROM dag_nodes WHERE task_id='TASK-R'").Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if revision != 1 {
		t.Fatalf("revision=%d want=1", revision)
	}
}
