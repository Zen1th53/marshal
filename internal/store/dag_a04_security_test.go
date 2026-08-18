package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/dag"
)

func TestT29A04StoreRejectsCycleAtCommitBoundary(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "marshal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	for _, id := range []dag.TaskID{"TASK-A", "TASK-B"} {
		if _, err := st.PutDAGNode(ctx, dag.Node{TaskID: id, Kind: dag.NodeKindTask, Status: dag.StatusPending}); err != nil {
			t.Fatalf("PutDAGNode(%s): %v", id, err)
		}
	}
	if _, err := st.PutDAGEdge(ctx, dag.Edge{From: "TASK-A", To: "TASK-B", Condition: dag.ConditionCompleted}); err != nil {
		t.Fatalf("PutDAGEdge(A->B): %v", err)
	}
	if _, err := st.PutDAGEdge(ctx, dag.Edge{From: "TASK-B", To: "TASK-A", Condition: dag.ConditionCompleted}); !errors.Is(err, dag.ErrCycle) {
		t.Fatalf("PutDAGEdge(B->A) error = %v, want DAG_CYCLE", err)
	}

	edges, err := st.DAGEdgesFrom(ctx, "TASK-B")
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 0 {
		t.Fatalf("cycle edge persisted: %#v", edges)
	}
}

func TestT29A04DependencyDeleteCannotLeaveDanglingEdge(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "marshal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []dag.TaskID{"TASK-PARENT", "TASK-CHILD"} {
		if _, err := st.PutDAGNode(ctx, dag.Node{TaskID: id, Kind: dag.NodeKindTask, Status: dag.StatusPending}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.PutDAGEdge(ctx, dag.Edge{From: "TASK-PARENT", To: "TASK-CHILD", Condition: dag.ConditionCompleted}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `DELETE FROM dag_nodes WHERE task_id = ?`, `TASK-PARENT`); err != nil {
		t.Fatalf("delete parent: %v", err)
	}
	edges, err := st.DAGEdgesTo(ctx, "TASK-CHILD")
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 0 {
		t.Fatalf("dangling dependency remained after cascade: %#v", edges)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM pragma_foreign_key_check"); got != 0 {
		t.Fatalf("foreign_key_check rows = %d, want 0", got)
	}
}

func TestT29A04ConcurrentOppositeEdgesCannotCommitCycle(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "marshal.db")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	for _, id := range []dag.TaskID{"TASK-A", "TASK-B"} {
		if _, err := first.PutDAGNode(ctx, dag.Node{TaskID: id, Kind: dag.NodeKindTask, Status: dag.StatusPending}); err != nil {
			t.Fatal(err)
		}
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		_, err := first.PutDAGEdge(ctx, dag.Edge{From: "TASK-A", To: "TASK-B", Condition: dag.ConditionCompleted})
		results <- err
	}()
	go func() {
		<-start
		_, err := second.PutDAGEdge(ctx, dag.Edge{From: "TASK-B", To: "TASK-A", Condition: dag.ConditionCompleted})
		results <- err
	}()
	close(start)
	err1, err2 := <-results, <-results
	if err1 == nil && err2 == nil {
		t.Fatal("both opposite edges committed; durable DAG contains a cycle")
	}

	ab, err := first.DAGEdgesFrom(ctx, "TASK-A")
	if err != nil {
		t.Fatal(err)
	}
	ba, err := first.DAGEdgesFrom(ctx, "TASK-B")
	if err != nil {
		t.Fatal(err)
	}
	if len(ab) > 0 && len(ba) > 0 {
		t.Fatalf("cycle persisted: A edges=%#v B edges=%#v", ab, ba)
	}
}
