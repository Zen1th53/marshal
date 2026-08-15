package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/evidence"
)

func (s *Store) PutNode(ctx context.Context, node evidence.Node) (evidence.Node, error) {
	if err := node.Validate(); err != nil {
		return evidence.Node{}, err
	}
	clean, err := s.sanitizer.SanitizeNode(ctx, node)
	if err != nil {
		return evidence.Node{}, err
	}
	digest, err := evidence.CanonicalDigest(clean.Type, clean.Metadata)
	if err != nil {
		return evidence.Node{}, err
	}
	if digest != clean.Digest {
		_ = s.recordEvidenceEvent(ctx, "evidence.digest.mismatch", map[string]any{
			"evidence_id": clean.ID, "action": evidence.ActionCreate,
			"reason_code": evidence.CodeDigestMismatch,
		})
		return evidence.Node{}, evidence.ErrDigestMismatch
	}
	metadata, err := json.Marshal(clean.Metadata)
	if err != nil {
		return evidence.Node{}, evidence.NewError(evidence.CodeSecretRejected, err)
	}
	created := clean.CreatedAt.UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return evidence.Node{}, fmt.Errorf("begin evidence node: %w", err)
	}
	defer tx.Rollback()
	var typ, digestStored, metadataStored, createdStored string
	err = tx.QueryRowContext(ctx, `SELECT node_type, digest, metadata_json, created_at FROM evidence_nodes WHERE node_id = ?`, clean.ID).Scan(&typ, &digestStored, &metadataStored, &createdStored)
	if err == nil {
		if typ == string(clean.Type) && digestStored == clean.Digest && metadataStored == string(metadata) && createdStored == created {
			return evidence.CloneNode(clean), nil
		}
		return evidence.Node{}, evidence.ErrImmutable
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return evidence.Node{}, fmt.Errorf("read evidence node: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO evidence_nodes(node_id, node_type, digest, metadata_json, created_at, state) VALUES(?, ?, ?, ?, ?, 'stored')`, clean.ID, clean.Type, clean.Digest, string(metadata), created); err != nil {
		return evidence.Node{}, fmt.Errorf("insert evidence node: %w", err)
	}
	if err := s.appendEvidenceEvent(ctx, tx, "evidence.node.stored", "", "", map[string]any{
		"evidence_id": clean.ID, "node_type": clean.Type, "state": evidence.StateStored,
		"content_digest": clean.Digest, "action": evidence.ActionCreate,
	}); err != nil {
		return evidence.Node{}, fmt.Errorf("record evidence node audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return evidence.Node{}, fmt.Errorf("commit evidence node: %w", err)
	}
	return evidence.CloneNode(clean), nil
}

func (s *Store) Get(ctx context.Context, id evidence.NodeID) (evidence.Node, error) {
	var node evidence.Node
	var typ, metadata, created string
	var state string
	err := s.db.QueryRowContext(ctx, `SELECT node_type, digest, metadata_json, created_at, state FROM evidence_nodes WHERE node_id = ?`, id).Scan(&typ, &node.Digest, &metadata, &created, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return evidence.Node{}, fmt.Errorf("evidence node %s: %w", id, evidence.ErrInvalidEdge)
	}
	if err != nil {
		return evidence.Node{}, fmt.Errorf("read evidence node: %w", err)
	}
	node.ID, node.Type = id, evidence.NodeType(typ)
	node.State = evidence.State(state)
	if err := json.Unmarshal([]byte(metadata), &node.Metadata); err != nil {
		return evidence.Node{}, fmt.Errorf("decode evidence metadata: %w", err)
	}
	node.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return evidence.Node{}, fmt.Errorf("parse evidence timestamp: %w", err)
	}
	return evidence.CloneNode(node), nil
}

// TransitionNode applies one legal lifecycle transition atomically. Repeating
// the current state is idempotent; all other illegal transitions fail closed.
func (s *Store) TransitionNode(ctx context.Context, id evidence.NodeID, target evidence.State) error {
	return evidence.ErrAuthorizationUnavailable
}

// TransitionNodeAuthorized enforces the external policy/authority decision
// immediately before a CAS-like state mutation. No configured authorizer is a
// deny, never an implicit allow.
func (s *Store) TransitionNodeAuthorized(ctx context.Context, request evidence.AccessRequest) error {
	if request.Action != evidence.ActionTransition || request.NodeID == "" {
		return evidence.ErrAuthorizationDenied
	}
	if s.authorizer == nil {
		return evidence.ErrAuthorizationUnavailable
	}
	current, err := s.Get(ctx, request.NodeID)
	if err != nil {
		return evidence.ErrAuthorizationDenied
	}
	request.CurrentState = current.State
	decision, err := s.authorizer.Authorize(ctx, request)
	if err != nil {
		return evidence.ErrAuthorizationUnavailable
	}
	if !decision.Allowed {
		return evidence.ErrAuthorizationDenied
	}
	if decision.SubjectID != request.SubjectID || decision.NodeID != request.NodeID || decision.TaskID != request.TaskID || decision.ChangeID != request.ChangeID {
		return evidence.ErrAuthorizationDenied
	}
	if decision.State != request.CurrentState || !evidence.ValidDigest(decision.PolicyDigest) ||
		!decision.FreshUntil.After(time.Now().UTC()) {
		return evidence.ErrAuthorizationStale
	}
	return s.transitionNodeIfState(ctx, request.NodeID, request.TargetState, request.CurrentState, request.SessionID, request.SessionRevision, request.SubjectID, request.TaskID, map[string]any{
		"evidence_id": request.NodeID, "previous_state": request.CurrentState,
		"new_state": request.TargetState, "subject_id": request.SubjectID,
		"task_id": request.TaskID, "change_id": request.ChangeID,
		"policy_digest": decision.PolicyDigest, "action": evidence.ActionTransition,
	})
}

func (s *Store) transitionNodeIfState(ctx context.Context, id evidence.NodeID, target, expected evidence.State, sessionID string, sessionRevision int64, subjectID, taskID string, audit map[string]any) error {
	if !target.Valid() || target == evidence.StateDraft {
		return evidence.ErrInvalidTransition
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin evidence transition: %w", err)
	}
	defer tx.Rollback()
	if sessionID != "" {
		var agentID, boundTask, status string
		var revision int64
		if err := tx.QueryRowContext(ctx, `
			SELECT agent_id, COALESCE(task_id, ''), status, revision
			FROM sessions WHERE session_id = ?
		`, sessionID).Scan(&agentID, &boundTask, &status, &revision); err != nil {
			return evidence.ErrAuthorizationStale
		}
		if status != "active" || agentID != subjectID || boundTask != taskID || revision != sessionRevision {
			return evidence.ErrAuthorizationStale
		}
	}
	valid := (expected == evidence.StateStored && target == evidence.StateLinked) ||
		(expected == evidence.StateLinked && target == evidence.StateArchived) ||
		(expected == evidence.StateArchived && target == evidence.StateExported) || expected == target
	if !valid {
		return evidence.ErrInvalidTransition
	}
	result, err := tx.ExecContext(ctx, `UPDATE evidence_nodes SET state = ? WHERE node_id = ? AND state = ?`, target, id, expected)
	if err != nil {
		return fmt.Errorf("update evidence state: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return evidence.ErrAuthorizationStale
	}
	if target != expected {
		// Subject/task identifiers remain correlation metadata here. The legacy
		// audit envelope's actor/task columns are foreign-keyed to runtime
		// identity rows, so unknown runtime identities must not be forged into
		// those columns.
		if err := s.appendEvidenceEvent(ctx, tx, "evidence.state.transitioned", "", "", audit); err != nil {
			return fmt.Errorf("record evidence transition audit: %w", err)
		}
	}
	return tx.Commit()
}

func (s *Store) Link(ctx context.Context, edge evidence.Edge) (evidence.Edge, error) {
	if err := edge.Validate(); err != nil {
		return evidence.Edge{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return evidence.Edge{}, fmt.Errorf("begin evidence edge: %w", err)
	}
	defer tx.Rollback()
	for _, id := range []evidence.NodeID{edge.From, edge.To} {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM evidence_nodes WHERE node_id = ?`, id).Scan(&exists); err != nil {
			return evidence.Edge{}, fmt.Errorf("check evidence node: %w", err)
		}
		if exists != 1 {
			return evidence.Edge{}, evidence.ErrInvalidEdge
		}
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO evidence_edges(from_node_id, to_node_id, relation, created_at) VALUES(?, ?, ?, ?) ON CONFLICT(from_node_id, to_node_id, relation) DO NOTHING`, edge.From, edge.To, edge.Relation, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return evidence.Edge{}, fmt.Errorf("insert evidence edge: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return evidence.Edge{}, fmt.Errorf("inspect evidence edge: %w", err)
	}
	if inserted == 1 {
		if err := s.appendEvidenceEvent(ctx, tx, "evidence.edge.linked", "", "", map[string]any{
			"from_evidence_id": edge.From, "to_evidence_id": edge.To,
			"relation": edge.Relation, "action": evidence.ActionLink,
		}); err != nil {
			return evidence.Edge{}, fmt.Errorf("record evidence edge audit: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return evidence.Edge{}, fmt.Errorf("commit evidence edge: %w", err)
	}
	return edge, nil
}

func (s *Store) Neighbors(ctx context.Context, id evidence.NodeID) ([]evidence.Node, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT to_node_id FROM evidence_edges WHERE from_node_id = ? ORDER BY to_node_id`, id)
	if err != nil {
		return nil, fmt.Errorf("query evidence neighbors: %w", err)
	}
	defer rows.Close()
	var nodes []evidence.Node
	for rows.Next() {
		var target evidence.NodeID
		if err := rows.Scan(&target); err != nil {
			return nil, err
		}
		node, err := s.Get(ctx, target)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}
