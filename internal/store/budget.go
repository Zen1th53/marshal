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

// SaveGoalTermination persists the final product state for a Goal execution.
func (s *Store) SaveGoalTermination(ctx context.Context, term model.GoalTermination) error {
	if err := term.Validate(); err != nil {
		return fmt.Errorf("%w: %v", model.ErrInvalid, err)
	}

	budgetJSON, err := json.Marshal(term.ConsumedBudget)
	if err != nil {
		return fmt.Errorf("%w: marshal consumed budget: %v", model.ErrInvalid, err)
	}

	for attempt := 0; ; attempt++ {
		err = s.saveGoalTerminationTx(ctx, term, string(budgetJSON))
		if !isSQLiteBusy(err) || attempt >= sqliteBusyRetries {
			return err
		}
		s.observeContention()
		if err := waitSQLiteRetry(ctx, attempt); err != nil {
			return err
		}
	}
}

func (s *Store) saveGoalTerminationTx(ctx context.Context, term model.GoalTermination, budgetJSON string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin save goal termination: %w", err)
	}
	defer tx.Rollback()

	completedAt := term.CompletedAt.UTC().Format(time.RFC3339Nano)

	query := `
		INSERT INTO goal_terminations (
			session_id, goal_id, revision, state, reason_code,
			reason_detail, budget_consumed_json, checkpoint_id, completed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id, goal_id, revision) DO UPDATE SET
			state = excluded.state,
			reason_code = excluded.reason_code,
			reason_detail = excluded.reason_detail,
			budget_consumed_json = excluded.budget_consumed_json,
			checkpoint_id = excluded.checkpoint_id,
			completed_at = excluded.completed_at
	`
	if _, err := tx.ExecContext(ctx, query,
		term.SessionID,
		term.GoalID,
		term.GoalRevision,
		string(term.State),
		string(term.ReasonCode),
		term.ReasonDetail,
		budgetJSON,
		term.CheckpointID,
		completedAt,
	); err != nil {
		return fmt.Errorf("insert goal termination: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit save goal termination: %w", err)
	}
	return nil
}

// GetGoalTermination retrieves a GoalTermination record by session, goal, and revision.
func (s *Store) GetGoalTermination(ctx context.Context, sessionID, goalID string, revision int64) (*model.GoalTermination, error) {
	query := `
		SELECT
			session_id, goal_id, revision, state, reason_code,
			reason_detail, budget_consumed_json, checkpoint_id, completed_at
		FROM goal_terminations
		WHERE session_id = ? AND goal_id = ? AND revision = ?
	`
	row := s.db.QueryRowContext(ctx, query, sessionID, goalID, revision)

	var (
		term        model.GoalTermination
		stateStr    string
		reasonStr   string
		budgetJSON  string
		completedAt string
	)

	err := row.Scan(
		&term.SessionID,
		&term.GoalID,
		&term.GoalRevision,
		&stateStr,
		&reasonStr,
		&term.ReasonDetail,
		&budgetJSON,
		&term.CheckpointID,
		&completedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: goal termination not found", model.ErrNotFound)
		}
		return nil, fmt.Errorf("query goal termination: %w", err)
	}

	term.State = model.TerminationState(stateStr)
	term.ReasonCode = model.ReasonCode(reasonStr)

	if budgetJSON != "" && budgetJSON != "{}" {
		if err := json.Unmarshal([]byte(budgetJSON), &term.ConsumedBudget); err != nil {
			return nil, fmt.Errorf("unmarshal budget consumed: %w", err)
		}
	}

	parsedCompletedAt, err := time.Parse(time.RFC3339Nano, completedAt)
	if err != nil {
		parsedCompletedAt, err = time.Parse(time.RFC3339, completedAt)
		if err != nil {
			return nil, fmt.Errorf("parse completed_at: %w", err)
		}
	}
	term.CompletedAt = parsedCompletedAt

	return &term, nil
}

// SaveBudgetTracker persists cumulative consumed budget for an active goal revision.
func (s *Store) SaveBudgetTracker(ctx context.Context, sessionID, goalID string, revision int64, consumed model.ConsumedBudget) error {
	consumedJSON, err := json.Marshal(consumed)
	if err != nil {
		return fmt.Errorf("%w: marshal consumed budget: %v", model.ErrInvalid, err)
	}

	for attempt := 0; ; attempt++ {
		err = s.saveBudgetTrackerTx(ctx, sessionID, goalID, revision, string(consumedJSON))
		if !isSQLiteBusy(err) || attempt >= sqliteBusyRetries {
			return err
		}
		s.observeContention()
		if err := waitSQLiteRetry(ctx, attempt); err != nil {
			return err
		}
	}
}

func (s *Store) saveBudgetTrackerTx(ctx context.Context, sessionID, goalID string, revision int64, consumedJSON string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin save budget tracker: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339Nano)

	query := `
		INSERT INTO budget_trackers (
			session_id, goal_id, revision, consumed_json, updated_at
		) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(session_id, goal_id, revision) DO UPDATE SET
			consumed_json = excluded.consumed_json,
			updated_at = excluded.updated_at
	`
	if _, err := tx.ExecContext(ctx, query, sessionID, goalID, revision, consumedJSON, now); err != nil {
		return fmt.Errorf("insert budget tracker: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit save budget tracker: %w", err)
	}
	return nil
}

// GetBudgetTracker retrieves the cumulative consumed budget for a goal revision.
func (s *Store) GetBudgetTracker(ctx context.Context, sessionID, goalID string, revision int64) (*model.ConsumedBudget, error) {
	query := `
		SELECT consumed_json
		FROM budget_trackers
		WHERE session_id = ? AND goal_id = ? AND revision = ?
	`
	row := s.db.QueryRowContext(ctx, query, sessionID, goalID, revision)

	var consumedJSON string
	err := row.Scan(&consumedJSON)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: budget tracker not found", model.ErrNotFound)
		}
		return nil, fmt.Errorf("query budget tracker: %w", err)
	}

	var consumed model.ConsumedBudget
	if consumedJSON != "" && consumedJSON != "{}" {
		if err := json.Unmarshal([]byte(consumedJSON), &consumed); err != nil {
			return nil, fmt.Errorf("unmarshal budget tracker: %w", err)
		}
	}

	return &consumed, nil
}
