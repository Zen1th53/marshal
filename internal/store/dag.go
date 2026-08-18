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
	// The cycle predicate and insert are one SQLite statement so no service-level
	// precheck can become a TOCTOU bypass. A candidate From->To edge is legal
	// only when To cannot already reach From in the durable graph snapshot used
	// by this write statement.
	result, err := tx.ExecContext(ctx, `
		WITH RECURSIVE reachable(task_id) AS (
			SELECT to_task FROM dag_edges WHERE from_task = ?
			UNION
			SELECT e.to_task
			FROM dag_edges e
			JOIN reachable r ON e.from_task = r.task_id
		)
		INSERT INTO dag_edges(from_task, to_task, condition, created_at)
		SELECT ?, ?, ?, ?
		WHERE NOT EXISTS (
			SELECT 1 FROM reachable WHERE task_id = ?
		)
	`, edge.To, edge.From, edge.To, edge.Condition, utcNow(), edge.From)
	if err != nil {
		return dag.Edge{}, fmt.Errorf("insert dag edge: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return dag.Edge{}, fmt.Errorf("inspect dag edge insert: %w", err)
	}
	if inserted != 1 {
		return dag.Edge{}, dag.ErrCycle
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

// TransitionDAGNode applies one canonical lifecycle edge with an expected-state
// compare-and-swap. Exact retry after a committed transition reconciles to the
// canonical target; stale or contradictory writers cannot overwrite it.
func (s *Store) TransitionDAGNode(ctx context.Context, id dag.TaskID, expected, target dag.NodeStatus) (dag.Node, error) {
	for attempt := 0; ; attempt++ {
		node, err := s.transitionDAGNodeOnce(ctx, id, expected, target)
		if !isSQLiteBusy(err) || attempt >= sqliteBusyRetries {
			return node, err
		}
		if err := waitSQLiteRetry(ctx, attempt); err != nil {
			return dag.Node{}, err
		}
	}
}

func (s *Store) transitionDAGNodeOnce(ctx context.Context, id dag.TaskID, expected, target dag.NodeStatus) (dag.Node, error) {
	if err := ctx.Err(); err != nil {
		return dag.Node{}, dag.NewError(dag.CodeInvalidRequest, err)
	}
	if !dag.CanTransition(expected, target) {
		return dag.Node{}, dag.ErrInvalidNode
	}
	probe := dag.Node{TaskID: id, Kind: dag.NodeKindTask, Status: expected}
	if err := probe.Validate(); err != nil {
		return dag.Node{}, dag.ErrInvalidNode
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return dag.Node{}, fmt.Errorf("begin dag state transition: %w", err)
	}
	defer tx.Rollback()

	var node dag.Node
	var revision int64
	err = tx.QueryRowContext(ctx, `
		SELECT task_id, kind, status, priority, revision
		FROM dag_nodes WHERE task_id = ?
	`, id).Scan(&node.TaskID, &node.Kind, &node.Status, &node.Priority, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return dag.Node{}, dag.ErrNodeNotFound
	}
	if err != nil {
		return dag.Node{}, fmt.Errorf("read dag node before transition: %w", err)
	}
	if err := node.Validate(); err != nil {
		return dag.Node{}, err
	}
	if node.Status == target {
		if err := tx.Commit(); err != nil {
			return dag.Node{}, fmt.Errorf("commit dag transition retry: %w", err)
		}
		return node, nil
	}
	if node.Status != expected {
		return dag.Node{}, fmt.Errorf("%w: dag node state changed", model.ErrConflict)
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE dag_nodes
		SET status = ?, revision = revision + 1, updated_at = ?
		WHERE task_id = ? AND status = ? AND revision = ?
	`, target, utcNow(), id, expected, revision)
	if err != nil {
		return dag.Node{}, fmt.Errorf("update dag node state: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return dag.Node{}, fmt.Errorf("read dag transition result: %w", err)
	}
	if rows != 1 {
		return dag.Node{}, fmt.Errorf("%w: dag node state changed", model.ErrConflict)
	}
	node.Status = target
	if err := tx.Commit(); err != nil {
		return dag.Node{}, fmt.Errorf("commit dag node transition: %w", err)
	}
	return node, nil
}

// DAGNodes returns a detached snapshot of canonical DAG nodes. Service layers
// use it for deterministic topological queries; callers cannot mutate storage.
func (s *Store) DAGNodes(ctx context.Context) ([]dag.Node, error) {
	if err := ctx.Err(); err != nil {
		return nil, dag.NewError(dag.CodeInvalidRequest, err)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT task_id, kind, status, priority
		FROM dag_nodes ORDER BY task_id
	`)
	if err != nil {
		return nil, fmt.Errorf("query dag nodes: %w", err)
	}
	defer rows.Close()
	var result []dag.Node
	for rows.Next() {
		var node dag.Node
		if err := rows.Scan(&node.TaskID, &node.Kind, &node.Status, &node.Priority); err != nil {
			return nil, fmt.Errorf("scan dag node: %w", err)
		}
		if err := node.Validate(); err != nil {
			return nil, err
		}
		result = append(result, node)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dag nodes: %w", err)
	}
	return result, nil
}
