package store

import (
	"context"
	"errors"
	"github.com/Zen1th53/marshal/internal/dag"
	"github.com/Zen1th53/marshal/internal/model"
	"path/filepath"
	"sync"
	"testing"
)

func TestT29A08TwoStoreTransitionsHaveOneWinnerAfterRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "dag-a08.db")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := first.PutDAGNode(ctx, dag.Node{TaskID: "TASK-A08", Kind: dag.NodeKindTask, Status: dag.StatusPending}); err != nil {
		t.Fatal(err)
	}
	if _, err := first.TransitionDAGNode(ctx, "TASK-A08", dag.StatusPending, dag.StatusReady); err != nil {
		t.Fatal(err)
	}
	if _, err := first.TransitionDAGNode(ctx, "TASK-A08", dag.StatusReady, dag.StatusRunning); err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	defer second.Close()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, st := range []*Store{first, second} {
		wg.Add(1)
		go func(st *Store) {
			defer wg.Done()
			_, err := st.TransitionDAGNode(ctx, "TASK-A08", dag.StatusRunning, dag.StatusCompleted)
			results <- err
		}(st)
	}
	wg.Wait()
	close(results)
	var success, conflict int
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, model.ErrConflict) {
			conflict++
		} else {
			t.Fatalf("unexpected transition error: %v", err)
		}
	}
	if success < 1 || success+conflict != 2 {
		t.Fatalf("success=%d conflict=%d", success, conflict)
	}
	loaded, err := second.GetDAGNode(ctx, "TASK-A08")
	if err != nil || loaded.Status != dag.StatusCompleted {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
}
