package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// SaveHandoffCheckpoint persists a durable handoff checkpoint with SQLite retry handling.
func (s *Store) SaveHandoffCheckpoint(ctx context.Context, cp model.HandoffCheckpoint) error {
	if err := cp.Validate(); err != nil {
		return fmt.Errorf("%w: %v", model.ErrInvalid, err)
	}

	taskSlotsJSON, err := json.Marshal(cp.TaskSlots)
	if err != nil {
		return fmt.Errorf("%w: marshal task slots: %v", model.ErrInvalid, err)
	}
	claimsJSON, err := json.Marshal(cp.ClaimIDs)
	if err != nil {
		return fmt.Errorf("%w: marshal claim IDs: %v", model.ErrInvalid, err)
	}
	evidenceJSON, err := json.Marshal(cp.EvidenceIDs)
	if err != nil {
		return fmt.Errorf("%w: marshal evidence IDs: %v", model.ErrInvalid, err)
	}
	budgetJSON, err := json.Marshal(cp.BudgetState)
	if err != nil {
		return fmt.Errorf("%w: marshal budget state: %v", model.ErrInvalid, err)
	}
	blockersJSON, err := json.Marshal(cp.PendingBlockers)
	if err != nil {
		return fmt.Errorf("%w: marshal pending blockers: %v", model.ErrInvalid, err)
	}
	snapshotJSON, err := json.Marshal(cp.StateSnapshot)
	if err != nil {
		return fmt.Errorf("%w: marshal state snapshot: %v", model.ErrInvalid, err)
	}

	for attempt := 0; ; attempt++ {
		err = s.saveHandoffCheckpointTx(ctx, cp, string(taskSlotsJSON), string(claimsJSON),
			string(evidenceJSON), string(budgetJSON), string(blockersJSON), string(snapshotJSON))
		if !isSQLiteBusy(err) || attempt >= sqliteBusyRetries {
			return err
		}
		s.observeContention()
		if err := waitSQLiteRetry(ctx, attempt); err != nil {
			return err
		}
	}
}

func (s *Store) saveHandoffCheckpointTx(
	ctx context.Context,
	cp model.HandoffCheckpoint,
	taskSlotsJSON, claimsJSON, evidenceJSON, budgetJSON, blockersJSON, snapshotJSON string,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin save checkpoint: %w", err)
	}
	defer tx.Rollback()

	createdAt := cp.CreatedAt.UTC().Format(time.RFC3339Nano)
	if cp.Version <= 0 {
		cp.Version = 1
	}

	query := `
		INSERT INTO handoff_checkpoints (
			checkpoint_id, version, goal_id, goal_revision, constraints_digest,
			task_id, session_id, handoff_id, agent_id, harness, model, role,
			branch, worktree_path, base_head, result_head, tree_sha, diff_digest,
			last_consumed_cursor, task_slots_json, claims_json, evidence_refs_json,
			budget_state_json, pending_blockers_json, state_snapshot_json, reason, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(checkpoint_id) DO UPDATE SET
			goal_id = excluded.goal_id,
			goal_revision = excluded.goal_revision,
			constraints_digest = excluded.constraints_digest,
			task_id = excluded.task_id,
			session_id = excluded.session_id,
			handoff_id = excluded.handoff_id,
			agent_id = excluded.agent_id,
			harness = excluded.harness,
			model = excluded.model,
			role = excluded.role,
			branch = excluded.branch,
			worktree_path = excluded.worktree_path,
			base_head = excluded.base_head,
			result_head = excluded.result_head,
			tree_sha = excluded.tree_sha,
			diff_digest = excluded.diff_digest,
			last_consumed_cursor = excluded.last_consumed_cursor,
			task_slots_json = excluded.task_slots_json,
			claims_json = excluded.claims_json,
			evidence_refs_json = excluded.evidence_refs_json,
			budget_state_json = excluded.budget_state_json,
			pending_blockers_json = excluded.pending_blockers_json,
			state_snapshot_json = excluded.state_snapshot_json,
			reason = excluded.reason
	`

	_, err = tx.ExecContext(ctx, query,
		cp.ID, cp.Version, cp.GoalID, cp.GoalRevision, cp.ConstraintsDigest,
		cp.TaskID, cp.SessionID, cp.HandoffID, cp.Author.AgentID, cp.Author.Harness,
		cp.Author.Model, cp.Role, cp.Branch, cp.WorktreePath, cp.BaseHEAD, cp.ResultHEAD,
		cp.TreeSHA, cp.DiffDigest, cp.LastCursor, taskSlotsJSON, claimsJSON, evidenceJSON,
		budgetJSON, blockersJSON, snapshotJSON, cp.Reason, createdAt)
	if err != nil {
		return fmt.Errorf("upsert handoff checkpoint: %w", err)
	}

	return tx.Commit()
}

// GetHandoffCheckpoint loads a specific checkpoint by ID.
func (s *Store) GetHandoffCheckpoint(ctx context.Context, id string) (model.HandoffCheckpoint, error) {
	query := `
		SELECT checkpoint_id, version, goal_id, goal_revision, constraints_digest,
		       task_id, session_id, handoff_id, agent_id, harness, model, role,
		       branch, worktree_path, base_head, result_head, tree_sha, diff_digest,
		       last_consumed_cursor, task_slots_json, claims_json, evidence_refs_json,
		       budget_state_json, pending_blockers_json, state_snapshot_json, reason, created_at
		FROM handoff_checkpoints
		WHERE checkpoint_id = ?
	`
	row := s.db.QueryRowContext(ctx, query, id)
	return scanCheckpoint(row)
}

// GetLatestHandoffCheckpoint returns the most recent checkpoint for a task.
func (s *Store) GetLatestHandoffCheckpoint(ctx context.Context, taskID string) (model.HandoffCheckpoint, error) {
	query := `
		SELECT checkpoint_id, version, goal_id, goal_revision, constraints_digest,
		       task_id, session_id, handoff_id, agent_id, harness, model, role,
		       branch, worktree_path, base_head, result_head, tree_sha, diff_digest,
		       last_consumed_cursor, task_slots_json, claims_json, evidence_refs_json,
		       budget_state_json, pending_blockers_json, state_snapshot_json, reason, created_at
		FROM handoff_checkpoints
		WHERE task_id = ?
		ORDER BY created_at DESC
		LIMIT 1
	`
	row := s.db.QueryRowContext(ctx, query, taskID)
	return scanCheckpoint(row)
}

// ListHandoffCheckpoints returns all checkpoints for a task ordered chronologically.
func (s *Store) ListHandoffCheckpoints(ctx context.Context, taskID string) ([]model.HandoffCheckpoint, error) {
	query := `
		SELECT checkpoint_id, version, goal_id, goal_revision, constraints_digest,
		       task_id, session_id, handoff_id, agent_id, harness, model, role,
		       branch, worktree_path, base_head, result_head, tree_sha, diff_digest,
		       last_consumed_cursor, task_slots_json, claims_json, evidence_refs_json,
		       budget_state_json, pending_blockers_json, state_snapshot_json, reason, created_at
		FROM handoff_checkpoints
		WHERE task_id = ?
		ORDER BY created_at ASC
	`
	rows, err := s.db.QueryContext(ctx, query, taskID)
	if err != nil {
		return nil, fmt.Errorf("list checkpoints: %w", err)
	}
	defer rows.Close()

	var list []model.HandoffCheckpoint
	for rows.Next() {
		cp, err := scanCheckpoint(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, cp)
	}
	return list, rows.Err()
}

// RecordCheckpointRollback records an audited rollback action in SQLite.
func (s *Store) RecordCheckpointRollback(ctx context.Context, rb model.CheckpointRollback) error {
	if err := rb.Validate(); err != nil {
		return fmt.Errorf("%w: %v", model.ErrInvalid, err)
	}

	actorJSON, err := json.Marshal(rb.Actor)
	if err != nil {
		return fmt.Errorf("%w: marshal actor: %v", model.ErrInvalid, err)
	}
	invalJSON, err := json.Marshal(rb.InvalidatedClaimIDs)
	if err != nil {
		return fmt.Errorf("%w: marshal invalidated claims: %v", model.ErrInvalid, err)
	}

	createdAt := rb.CreatedAt.UTC().Format(time.RFC3339Nano)

	query := `
		INSERT INTO checkpoint_rollbacks (
			rollback_id, checkpoint_id, from_checkpoint_id, reason,
			actor_provenance_json, invalidated_claim_ids_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`
	_, err = s.db.ExecContext(ctx, query,
		rb.RollbackID, rb.CheckpointID, rb.FromCheckpointID, rb.Reason,
		string(actorJSON), string(invalJSON), createdAt)
	if err != nil {
		return fmt.Errorf("record checkpoint rollback: %w", err)
	}

	return nil
}

// GetCheckpointRollbacks returns all rollback entries for a given checkpoint.
func (s *Store) GetCheckpointRollbacks(ctx context.Context, checkpointID string) ([]model.CheckpointRollback, error) {
	query := `
		SELECT rollback_id, checkpoint_id, from_checkpoint_id, reason,
		       actor_provenance_json, invalidated_claim_ids_json, created_at
		FROM checkpoint_rollbacks
		WHERE checkpoint_id = ?
		ORDER BY created_at ASC
	`
	rows, err := s.db.QueryContext(ctx, query, checkpointID)
	if err != nil {
		return nil, fmt.Errorf("get checkpoint rollbacks: %w", err)
	}
	defer rows.Close()

	var list []model.CheckpointRollback
	for rows.Next() {
		var rb model.CheckpointRollback
		var actorJSON, invalJSON, createdStr string
		if err := rows.Scan(&rb.RollbackID, &rb.CheckpointID, &rb.FromCheckpointID,
			&rb.Reason, &actorJSON, &invalJSON, &createdStr); err != nil {
			return nil, fmt.Errorf("scan rollback: %w", err)
		}
		if parsed, err := time.Parse(time.RFC3339Nano, createdStr); err == nil {
			rb.CreatedAt = parsed
		}
		if err := json.Unmarshal([]byte(actorJSON), &rb.Actor); err != nil {
			return nil, fmt.Errorf("decode actor: %w", err)
		}
		if err := json.Unmarshal([]byte(invalJSON), &rb.InvalidatedClaimIDs); err != nil {
			return nil, fmt.Errorf("decode invalidated claims: %w", err)
		}
		list = append(list, rb)
	}
	return list, rows.Err()
}

type cpScanner interface {
	Scan(dest ...any) error
}

func scanCheckpoint(scanner cpScanner) (model.HandoffCheckpoint, error) {
	var cp model.HandoffCheckpoint
	var slotsJSON, claimsJSON, evJSON, budgetJSON, blockersJSON, snapJSON, createdStr string

	err := scanner.Scan(
		&cp.ID, &cp.Version, &cp.GoalID, &cp.GoalRevision, &cp.ConstraintsDigest,
		&cp.TaskID, &cp.SessionID, &cp.HandoffID, &cp.Author.AgentID, &cp.Author.Harness,
		&cp.Author.Model, &cp.Role, &cp.Branch, &cp.WorktreePath, &cp.BaseHEAD,
		&cp.ResultHEAD, &cp.TreeSHA, &cp.DiffDigest, &cp.LastCursor,
		&slotsJSON, &claimsJSON, &evJSON, &budgetJSON, &blockersJSON, &snapJSON,
		&cp.Reason, &createdStr,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return model.HandoffCheckpoint{}, model.ErrNotFound
	}
	if err != nil {
		return model.HandoffCheckpoint{}, fmt.Errorf("scan checkpoint: %w", err)
	}

	if parsed, err := time.Parse(time.RFC3339Nano, createdStr); err == nil {
		cp.CreatedAt = parsed
	}

	if err := json.Unmarshal([]byte(slotsJSON), &cp.TaskSlots); err != nil {
		return model.HandoffCheckpoint{}, fmt.Errorf("decode task slots: %w", err)
	}
	if err := json.Unmarshal([]byte(claimsJSON), &cp.ClaimIDs); err != nil {
		return model.HandoffCheckpoint{}, fmt.Errorf("decode claims: %w", err)
	}
	if err := json.Unmarshal([]byte(evJSON), &cp.EvidenceIDs); err != nil {
		return model.HandoffCheckpoint{}, fmt.Errorf("decode evidence: %w", err)
	}
	if err := json.Unmarshal([]byte(budgetJSON), &cp.BudgetState); err != nil {
		return model.HandoffCheckpoint{}, fmt.Errorf("decode budget: %w", err)
	}
	if err := json.Unmarshal([]byte(blockersJSON), &cp.PendingBlockers); err != nil {
		return model.HandoffCheckpoint{}, fmt.Errorf("decode blockers: %w", err)
	}
	if err := json.Unmarshal([]byte(snapJSON), &cp.StateSnapshot); err != nil {
		return model.HandoffCheckpoint{}, fmt.Errorf("decode snapshot: %w", err)
	}

	return cp, nil
}
