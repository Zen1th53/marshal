package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Zen1th53/marshal/internal/dag"
	"github.com/Zen1th53/marshal/internal/model"
)

// PutDAGNode inserts one canonical DAG node. Exact retries are idempotent;
// conflicting immutable insertion fields are rejected. Lifecycle mutation is
// owned by T29.A03 and must not use this insertion path as a raw state setter.
func (s *Store) PutDAGNode(ctx context.Context, node dag.Node) (dag.Node, error) {
	if err := ctx.Err(); err != nil {
		return dag.Node{}, dag.NewError(dag.CodeInvalidRequest, err)
	}
	if err := node.Validate(); err != nil {
		return dag.Node{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return dag.Node{}, fmt.Errorf("begin dag node insert: %w", err)
	}
	defer tx.Rollback()

	var existing dag.Node
	err = tx.QueryRowContext(ctx, `
		SELECT task_id, kind, status, priority
		FROM dag_nodes WHERE task_id = ?
	`, node.TaskID).Scan(&existing.TaskID, &existing.Kind, &existing.Status, &existing.Priority)
	switch {
	case err == nil:
		if err := existing.Validate(); err != nil {
			return dag.Node{}, err
		}
		if existing != node {
			return dag.Node{}, fmt.Errorf("%w: dag node already exists with different immutable insertion fields", model.ErrConflict)
		}
		if err := tx.Commit(); err != nil {
			return dag.Node{}, fmt.Errorf("commit dag node retry: %w", err)
		}
		return existing, nil
	case !errors.Is(err, sql.ErrNoRows):
		return dag.Node{}, fmt.Errorf("read dag node before insert: %w", err)
	}

	now := utcNow()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO dag_nodes(task_id, kind, status, priority, revision, created_at, updated_at)
		VALUES(?, ?, ?, ?, 0, ?, ?)
	`, node.TaskID, node.Kind, node.Status, node.Priority, now, now); err != nil {
		return dag.Node{}, fmt.Errorf("insert dag node: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return dag.Node{}, fmt.Errorf("commit dag node insert: %w", err)
	}
	return node, nil
}

func (s *Store) GetDAGNode(ctx context.Context, id dag.TaskID) (dag.Node, error) {
	probe := dag.Node{TaskID: id, Kind: dag.NodeKindTask, Status: dag.StatusPending}
	if err := probe.Validate(); err != nil {
		return dag.Node{}, dag.ErrInvalidNode
	}
	var node dag.Node
	err := s.db.QueryRowContext(ctx, `
		SELECT task_id, kind, status, priority
		FROM dag_nodes WHERE task_id = ?
	`, id).Scan(&node.TaskID, &node.Kind, &node.Status, &node.Priority)
	if errors.Is(err, sql.ErrNoRows) {
		return dag.Node{}, dag.ErrNodeNotFound
	}
	if err != nil {
		return dag.Node{}, fmt.Errorf("read dag node: %w", err)
	}
	if err := node.Validate(); err != nil {
		return dag.Node{}, err
	}
	return node, nil
}

// PutDAGEdge inserts one directed dependency. Endpoint pairs are unique; a
// retry is surfaced as the stable duplicate-edge condition for the A03 service
// to reconcile using its request identity.
func (s *Store) PutDAGEdge(ctx context.Context, edge dag.Edge) (dag.Edge, error) {
	if err := ctx.Err(); err != nil {
		return dag.Edge{}, dag.NewError(dag.CodeInvalidRequest, err)
	}
	if err := edge.Validate(); err != nil {
		return dag.Edge{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return dag.Edge{}, fmt.Errorf("begin dag edge insert: %w", err)
	}
	defer tx.Rollback()

	for _, id := range []dag.TaskID{edge.From, edge.To} {
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM dag_nodes WHERE task_id = ?", id).Scan(&count); err != nil {
			return dag.Edge{}, fmt.Errorf("check dag edge endpoint: %w", err)
		}
		if count != 1 {
			return dag.Edge{}, dag.ErrNodeNotFound
		}
	}

	var condition dag.EdgeCondition
	err = tx.QueryRowContext(ctx, `SELECT condition FROM dag_edges WHERE from_task = ? AND to_task = ?`, edge.From, edge.To).Scan(&condition)
	switch {
	case err == nil:
		return dag.Edge{}, dag.ErrDuplicateEdge
	case !errors.Is(err, sql.ErrNoRows):
		return dag.Edge{}, fmt.Errorf("read dag edge before insert: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO dag_edges(from_task, to_task, condition, created_at)
		VALUES(?, ?, ?, ?)
	`, edge.From, edge.To, edge.Condition, utcNow()); err != nil {
		return dag.Edge{}, fmt.Errorf("insert dag edge: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return dag.Edge{}, fmt.Errorf("commit dag edge insert: %w", err)
	}
	return edge, nil
}

func (s *Store) DAGEdgesFrom(ctx context.Context, from dag.TaskID) ([]dag.Edge, error) {
	return s.queryDAGEdges(ctx, `
		SELECT from_task, to_task, condition FROM dag_edges
		WHERE from_task = ? ORDER BY to_task, condition
	`, from)
}

func (s *Store) DAGEdgesTo(ctx context.Context, to dag.TaskID) ([]dag.Edge, error) {
	return s.queryDAGEdges(ctx, `
		SELECT from_task, to_task, condition FROM dag_edges
		WHERE to_task = ? ORDER BY from_task, condition
	`, to)
}

func (s *Store) queryDAGEdges(ctx context.Context, query string, id dag.TaskID) ([]dag.Edge, error) {
	probe := dag.Node{TaskID: id, Kind: dag.NodeKindTask, Status: dag.StatusPending}
	if err := probe.Validate(); err != nil {
		return nil, dag.ErrInvalidNode
	}
	rows, err := s.db.QueryContext(ctx, query, id)
	if err != nil {
		return nil, fmt.Errorf("query dag edges: %w", err)
	}
	defer rows.Close()
	var result []dag.Edge
	for rows.Next() {
		var edge dag.Edge
		if err := rows.Scan(&edge.From, &edge.To, &edge.Condition); err != nil {
			return nil, fmt.Errorf("scan dag edge: %w", err)
		}
		if err := edge.Validate(); err != nil {
			return nil, err
		}
		result = append(result, edge)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dag edges: %w", err)
	}
	return result, nil
}
