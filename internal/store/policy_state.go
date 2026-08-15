package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policy"
)

// TransitionPolicyState applies one explicit lifecycle transition using a
// canonical compare-and-set boundary. Lifecycle validity is not authorization;
// A04 owns the privileged caller boundary.
func (s *Store) TransitionPolicyState(ctx context.Context, id policy.PolicyID, version policy.PolicyVersion, from, to policy.State) (PolicyRecord, error) {
	for attempt := 0; ; attempt++ {
		record, err := s.transitionPolicyStateOnce(ctx, id, version, from, to)
		if err == nil || !isSQLiteBusy(err) || attempt >= sqliteBusyRetries {
			return record, err
		}
		if err := waitSQLiteRetry(ctx, attempt); err != nil {
			return PolicyRecord{}, err
		}
	}
}

func (s *Store) transitionPolicyStateOnce(ctx context.Context, id policy.PolicyID, version policy.PolicyVersion, from, to policy.State) (PolicyRecord, error) {
	if version <= 0 || !from.Valid() || !to.Valid() || from == to {
		return PolicyRecord{}, fmt.Errorf("%w: invalid policy lifecycle transition", model.ErrInvalid)
	}
	if !policy.CanTransition(from, to) {
		return PolicyRecord{}, fmt.Errorf("%w: illegal policy lifecycle transition", model.ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PolicyRecord{}, fmt.Errorf("begin policy transition: %w", err)
	}
	defer tx.Rollback()
	var current string
	var generation int64
	err = tx.QueryRowContext(ctx, `
		SELECT state, generation FROM policy_versions WHERE policy_id = ? AND version = ?
	`, string(id), int64(version)).Scan(&current, &generation)
	if errors.Is(err, sql.ErrNoRows) {
		return PolicyRecord{}, fmt.Errorf("%w: policy version not found", model.ErrNotFound)
	}
	if err != nil {
		return PolicyRecord{}, fmt.Errorf("read policy lifecycle state: %w", err)
	}
	currentState := policy.State(current)
	if !currentState.Valid() {
		return PolicyRecord{}, fmt.Errorf("%w: invalid durable policy lifecycle state", model.ErrInvalid)
	}
	// A retry of a committed edge reconciles to the already-canonical target.
	if currentState == to {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			return PolicyRecord{}, fmt.Errorf("rollback idempotent policy transition: %w", err)
		}
		return s.GetPolicy(ctx, id, version)
	}
	if currentState != from {
		return PolicyRecord{}, fmt.Errorf("%w: stale policy lifecycle state", model.ErrConflict)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE policy_versions SET state = ?
		WHERE policy_id = ? AND version = ? AND state = ? AND generation = ?
	`, string(to), string(id), int64(version), string(from), generation)
	if err != nil {
		return PolicyRecord{}, fmt.Errorf("update policy lifecycle state: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return PolicyRecord{}, fmt.Errorf("check policy lifecycle transition: %w", err)
	}
	if rows != 1 {
		return PolicyRecord{}, fmt.Errorf("%w: stale policy lifecycle state", model.ErrConflict)
	}
	if err := tx.Commit(); err != nil {
		return PolicyRecord{}, fmt.Errorf("commit policy transition: %w", err)
	}
	return s.GetPolicy(ctx, id, version)
}
