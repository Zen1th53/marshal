package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/dag"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestA02DAGMigrationPersistsNodesEdgesAndIndexes(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if got := queryInt(t, st.db, "SELECT max(version) FROM schema_migrations"); got != LatestSchemaVersion {
		t.Fatalf("schema version=%d, want latest", got)
	}
	for _, table := range []string{"dag_nodes", "dag_edges"} {
		if got := queryInt(t, st.db, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table); got != 1 {
			t.Fatalf("table %s count=%d", table, got)
		}
	}
	for _, index := range []string{"dag_edges_by_from", "dag_edges_by_to"} {
		if got := queryInt(t, st.db, "SELECT count(*) FROM sqlite_master WHERE type='index' AND name=?", index); got != 1 {
			t.Fatalf("index %s count=%d", index, got)
		}
	}
}

func TestA02DAGNodeRoundTripIdempotencyAndConflict(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	node := dag.Node{TaskID: "TASK-A", Kind: dag.NodeKindTask, Status: dag.StatusPending, Priority: 7}
	if _, err := st.PutDAGNode(ctx, node); err != nil {
		t.Fatalf("PutDAGNode: %v", err)
	}
	if _, err := st.PutDAGNode(ctx, node); err != nil {
		t.Fatalf("idempotent PutDAGNode: %v", err)
	}
	loaded, err := st.GetDAGNode(ctx, node.TaskID)
	if err != nil {
		t.Fatalf("GetDAGNode: %v", err)
	}
	if loaded != node {
		t.Fatalf("loaded=%+v want=%+v", loaded, node)
	}
	conflict := node
	conflict.Priority = 8
	if _, err := st.PutDAGNode(ctx, conflict); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("conflict error=%v, want model.ErrConflict", err)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM dag_nodes WHERE task_id='TASK-A'"); got != 1 {
		t.Fatalf("node rows=%d", got)
	}
}

func TestA02DAGEdgeRoundTripReverseLookupAndDuplicate(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []dag.TaskID{"TASK-A", "TASK-B"} {
		if _, err := st.PutDAGNode(ctx, dag.Node{TaskID: id, Kind: dag.NodeKindTask, Status: dag.StatusPending}); err != nil {
			t.Fatal(err)
		}
	}
	edge := dag.Edge{From: "TASK-A", To: "TASK-B", Condition: dag.ConditionCompleted}
	if _, err := st.PutDAGEdge(ctx, edge); err != nil {
		t.Fatalf("PutDAGEdge: %v", err)
	}
	if _, err := st.PutDAGEdge(ctx, edge); !errors.Is(err, dag.ErrDuplicateEdge) {
		t.Fatalf("duplicate error=%v, want %v", err, dag.ErrDuplicateEdge)
	}
	outbound, err := st.DAGEdgesFrom(ctx, "TASK-A")
	if err != nil || len(outbound) != 1 || outbound[0] != edge {
		t.Fatalf("outbound=%+v err=%v", outbound, err)
	}
	inbound, err := st.DAGEdgesTo(ctx, "TASK-B")
	if err != nil || len(inbound) != 1 || inbound[0] != edge {
		t.Fatalf("inbound=%+v err=%v", inbound, err)
	}
}

func TestA02DAGRejectsForeignEdgeAndSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "dag.db")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := first.PutDAGNode(ctx, dag.Node{TaskID: "TASK-A", Kind: dag.NodeKindTask, Status: dag.StatusPending}); err != nil {
		t.Fatal(err)
	}
	if _, err := first.PutDAGEdge(ctx, dag.Edge{From: "TASK-A", To: "TASK-MISSING", Condition: dag.ConditionCompleted}); !errors.Is(err, dag.ErrNodeNotFound) {
		t.Fatalf("missing-node edge error=%v, want %v", err, dag.ErrNodeNotFound)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	loaded, err := second.GetDAGNode(ctx, "TASK-A")
	if err != nil || loaded.TaskID != "TASK-A" {
		t.Fatalf("restart loaded=%+v err=%v", loaded, err)
	}
	if got := queryInt(t, second.db, "SELECT count(*) FROM pragma_foreign_key_check"); got != 0 {
		t.Fatalf("foreign key violations=%d", got)
	}
	var integrity string
	if err := second.db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity=%q err=%v", integrity, err)
	}
}
