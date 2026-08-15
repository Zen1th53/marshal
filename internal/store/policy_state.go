package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policy"
)

// transitionPolicyState is the store-internal lifecycle primitive. It is not
// an authorization boundary and therefore is intentionally unexported.
func (s *Store) transitionPolicyState(ctx context.Context, id policy.PolicyID, version policy.PolicyVersion, from, to policy.State, binding *policy.PolicyBinding) (PolicyRecord, error) {
	return s.transitionPolicyStateWithEvent(ctx, id, version, from, to, binding, nil)
}

func (s *Store) transitionPolicyStateWithEvent(ctx context.Context, id policy.PolicyID, version policy.PolicyVersion, from, to policy.State, binding *policy.PolicyBinding, event *policy.PolicyMutationRequest) (PolicyRecord, error) {
	for attempt := 0; ; attempt++ {
		record, err := s.transitionPolicyStateOnce(ctx, id, version, from, to, binding, event)
		if err == nil || !isSQLiteBusy(err) || attempt >= sqliteBusyRetries {
			return record, err
		}
		if err := waitSQLiteRetry(ctx, attempt); err != nil {
			return PolicyRecord{}, err
		}
	}
}

func (s *Store) transitionPolicyStateOnce(ctx context.Context, id policy.PolicyID, version policy.PolicyVersion, from, to policy.State, binding *policy.PolicyBinding, event *policy.PolicyMutationRequest) (PolicyRecord, error) {
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
	var current, currentDigest string
	var generation int64
	err = tx.QueryRowContext(ctx, `
		SELECT state, generation, digest FROM policy_versions WHERE policy_id = ? AND version = ?
	`, string(id), int64(version)).Scan(&current, &generation, &currentDigest)
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
	if binding != nil && (currentDigest != string(binding.Digest) || uint64(generation) != binding.Generation) {
		return PolicyRecord{}, fmt.Errorf("%w: stale policy binding", model.ErrConflict)
	}
	// A retry of a committed edge reconciles to the already-canonical target.
	if currentState == to {
		if binding != nil {
			if event == nil {
				return PolicyRecord{}, fmt.Errorf("%w: stale policy lifecycle state", model.ErrConflict)
			}
			if err := s.appendPolicyEvent(ctx, tx, policyEventType(*event), *event, "allowed", string(policy.CodeAuthorizationAllowed)); err != nil {
				return PolicyRecord{}, err
			}
			if err := tx.Commit(); err != nil {
				return PolicyRecord{}, fmt.Errorf("commit policy retry reconciliation: %w", err)
			}
			return s.GetPolicy(ctx, id, version)
		}
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			return PolicyRecord{}, fmt.Errorf("rollback idempotent policy transition: %w", err)
		}
		return s.GetPolicy(ctx, id, version)
	}
	if currentState != from {
		return PolicyRecord{}, fmt.Errorf("%w: stale policy lifecycle state", model.ErrConflict)
	}
	if to == policy.StateActive {
		var activeCount int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM policy_versions WHERE state = ?`, string(policy.StateActive)).Scan(&activeCount); err != nil {
			return PolicyRecord{}, fmt.Errorf("count active policies: %w", err)
		}
		if activeCount != 0 {
			return PolicyRecord{}, fmt.Errorf("%w: another policy version is already active", model.ErrConflict)
		}
	}
	query := `UPDATE policy_versions SET state = ? WHERE policy_id = ? AND version = ? AND state = ? AND generation = ?`
	args := []any{string(to), string(id), int64(version), string(from), generation}
	if binding != nil {
		query += " AND digest = ?"
		args = append(args, string(binding.Digest))
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
			return PolicyRecord{}, fmt.Errorf("%w: active policy already exists", model.ErrConflict)
		}
		return PolicyRecord{}, fmt.Errorf("update policy lifecycle state: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return PolicyRecord{}, fmt.Errorf("check policy lifecycle transition: %w", err)
	}
	if rows != 1 {
		return PolicyRecord{}, fmt.Errorf("%w: stale policy lifecycle state", model.ErrConflict)
	}
	if event != nil {
		if err := s.appendPolicyEvent(ctx, tx, policyEventType(*event), *event, "allowed", string(policy.CodeAuthorizationAllowed)); err != nil {
			return PolicyRecord{}, fmt.Errorf("record policy mutation event: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
			return PolicyRecord{}, fmt.Errorf("%w: active policy already exists", model.ErrConflict)
		}
		return PolicyRecord{}, fmt.Errorf("commit policy transition: %w", err)
	}
	return s.GetPolicy(ctx, id, version)
}

// TransitionPolicyStateAuthorized is the sole exported policy lifecycle
// mutation boundary. It validates an exact, fresh management decision before
// invoking the store-internal A03 compare-and-set primitive.
func (s *Store) TransitionPolicyStateAuthorized(ctx context.Context, request policy.PolicyMutationRequest, authorizer policy.ManagementAuthorizer) (PolicyRecord, error) {
	if err := request.Validate(); err != nil {
		return PolicyRecord{}, err
	}
	if authorizer == nil {
		return PolicyRecord{}, policy.ErrAuthorizationUnavailable
	}
	decision, err := authorizer.AuthorizePolicyMutation(ctx, request)
	if err != nil {
		return PolicyRecord{}, policy.ErrAuthorizationUnavailable
	}
	if err := decision.ValidateFor(request); err != nil {
		if errors.Is(err, policy.ErrAuthorizationDenied) {
			_ = s.recordPolicyDeniedEvent(ctx, request)
		}
		return PolicyRecord{}, err
	}
	return s.transitionPolicyStateWithEvent(ctx, request.PolicyID, request.PolicyVersion, request.ExpectedState, request.TargetState, &request.Binding, &request)
}
