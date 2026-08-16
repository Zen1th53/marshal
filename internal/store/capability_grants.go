package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policy"
)

// PutCapabilityGrant stores a validated immutable grant. Repeating the exact
// grant is idempotent; changing an existing grant is a conflict.
func (s *Store) PutCapabilityGrant(ctx context.Context, grant capability.Grant) error {
	if err := grant.Validate(); err != nil {
		return err
	}
	if grant.PolicyDigest != "" {
		if err := policy.PolicyDigest(grant.PolicyDigest).Validate(); err != nil {
			return fmt.Errorf("%w: invalid capability policy digest", model.ErrInvalid)
		}
	}
	actions, constraints, err := marshalCapabilityScope(grant.Scope)
	if err != nil {
		return fmt.Errorf("%w: encode capability scope", model.ErrInvalid)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO capability_grants(
			id, subject, task_id, kind, resource, actions_json, constraints_json,
			issuer, issued_at, expires_at, revoked_at, policy_digest, idempotency_key
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, string(grant.ID), string(grant.Subject), string(grant.TaskID), string(grant.Kind), grant.Scope.Resource,
		string(actions), string(constraints), string(grant.Issuer), grant.IssuedAt.UTC().Format(time.RFC3339Nano),
		grant.ExpiresAt.UTC().Format(time.RFC3339Nano), formatOptionalTime(grant.RevokedAt), grant.PolicyDigest, grant.IdempotencyKey)
	if err == nil {
		return nil
	}
	if !strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
		return fmt.Errorf("persist capability grant: %w", err)
	}
	stored, loadErr := s.GetCapabilityGrant(ctx, grant.ID)
	if loadErr != nil {
		if existing, found, findErr := s.FindCapabilityGrantByIdempotencyKey(ctx, grant.IdempotencyKey); findErr == nil && found && existing.ID != grant.ID {
			return fmt.Errorf("%w: capability idempotency key is already bound", model.ErrConflict)
		}
		return fmt.Errorf("read existing capability grant: %w", loadErr)
	}
	if capabilityGrantsEqual(stored, grant) {
		return nil
	}
	return fmt.Errorf("%w: capability grant is immutable", model.ErrConflict)
}

func (s *Store) GetCapabilityGrant(ctx context.Context, id capability.GrantID) (capability.Grant, error) {
	var grant capability.Grant
	var subject, taskID, kind, resource, actionsJSON, constraintsJSON, issuer, issuedAt, expiresAt, revokedAt, digest string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, subject, task_id, kind, resource, actions_json, constraints_json,
		       issuer, issued_at, expires_at, COALESCE(revoked_at, ''), policy_digest, idempotency_key
		FROM capability_grants WHERE id = ?
	`, string(id)).Scan(&grant.ID, &subject, &taskID, &kind, &resource, &actionsJSON, &constraintsJSON,
		&issuer, &issuedAt, &expiresAt, &revokedAt, &digest, &grant.IdempotencyKey)
	if errors.Is(err, sql.ErrNoRows) {
		return capability.Grant{}, fmt.Errorf("%w: capability grant not found", model.ErrNotFound)
	}
	if err != nil {
		return capability.Grant{}, fmt.Errorf("read capability grant: %w", err)
	}
	grant.Subject, grant.TaskID, grant.Kind = capability.SubjectID(subject), capability.TaskID(taskID), capability.CapabilityKind(kind)
	grant.Scope.Resource = resource
	if err := json.Unmarshal([]byte(actionsJSON), &grant.Scope.Actions); err != nil {
		return capability.Grant{}, fmt.Errorf("%w: invalid capability actions", model.ErrInvalid)
	}
	if err := json.Unmarshal([]byte(constraintsJSON), &grant.Scope.Constraints); err != nil {
		return capability.Grant{}, fmt.Errorf("%w: invalid capability constraints", model.ErrInvalid)
	}
	grant.Issuer = capability.SubjectID(issuer)
	var parseErr error
	grant.IssuedAt, parseErr = time.Parse(time.RFC3339Nano, issuedAt)
	if parseErr != nil {
		return capability.Grant{}, fmt.Errorf("%w: invalid capability issue time", model.ErrInvalid)
	}
	grant.ExpiresAt, parseErr = time.Parse(time.RFC3339Nano, expiresAt)
	if parseErr != nil {
		return capability.Grant{}, fmt.Errorf("%w: invalid capability expiry time", model.ErrInvalid)
	}
	if revokedAt != "" {
		value, err := time.Parse(time.RFC3339Nano, revokedAt)
		if err != nil {
			return capability.Grant{}, fmt.Errorf("%w: invalid capability revoke time", model.ErrInvalid)
		}
		grant.RevokedAt = &value
	}
	grant.PolicyDigest = digest
	if grant.PolicyDigest != "" {
		if err := policy.PolicyDigest(grant.PolicyDigest).Validate(); err != nil {
			return capability.Grant{}, fmt.Errorf("%w: invalid durable capability policy digest", model.ErrInvalid)
		}
	}
	if err := grant.Validate(); err != nil {
		return capability.Grant{}, fmt.Errorf("%w: invalid durable capability grant", model.ErrInvalid)
	}
	return grant, nil
}

func (s *Store) FindCapabilityGrantByIdempotencyKey(ctx context.Context, key string) (capability.Grant, bool, error) {
	if strings.TrimSpace(key) == "" {
		return capability.Grant{}, false, nil
	}
	var id string
	err := s.db.QueryRowContext(ctx, "SELECT id FROM capability_grants WHERE idempotency_key = ?", key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return capability.Grant{}, false, nil
	}
	if err != nil {
		return capability.Grant{}, false, fmt.Errorf("find capability idempotency key: %w", err)
	}
	grant, err := s.GetCapabilityGrant(ctx, capability.GrantID(id))
	return grant, err == nil, err
}

func (s *Store) ListCapabilityGrants(ctx context.Context) ([]capability.Grant, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM capability_grants ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("list capability grants: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan capability grant id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list capability grants rows: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close capability grants rows: %w", err)
	}
	result := make([]capability.Grant, 0, len(ids))
	for _, id := range ids {
		grant, err := s.GetCapabilityGrant(ctx, capability.GrantID(id))
		if err != nil {
			return nil, err
		}
		result = append(result, grant)
	}
	return result, nil
}

// RevokeCapabilityGrant performs a compare-and-set revocation transition.
func (s *Store) RevokeCapabilityGrant(ctx context.Context, id capability.GrantID, revokedAt time.Time) error {
	if revokedAt.IsZero() {
		return fmt.Errorf("%w: revoke time is required", model.ErrInvalid)
	}
	grant, err := s.GetCapabilityGrant(ctx, id)
	if err != nil {
		return err
	}
	if !revokedAt.After(grant.IssuedAt) {
		return fmt.Errorf("%w: revoke time precedes grant issuance", model.ErrInvalid)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE capability_grants SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, revokedAt.UTC().Format(time.RFC3339Nano), string(id))
	if err != nil {
		return fmt.Errorf("revoke capability grant: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("revoke capability grant result: %w", err)
	}
	if rows == 1 {
		return nil
	}
	return fmt.Errorf("%w: capability grant is missing or already revoked", model.ErrConflict)
}

func marshalCapabilityScope(scope capability.Scope) ([]byte, []byte, error) {
	actions := append([]string(nil), scope.Actions...)
	for i := range actions {
		actions[i] = strings.TrimSpace(actions[i])
	}
	sort.Strings(actions)
	constraints := scope.Constraints
	if constraints == nil {
		constraints = map[string]string{}
	}
	actionsJSON, err := json.Marshal(actions)
	if err != nil {
		return nil, nil, err
	}
	constraintsJSON, err := json.Marshal(constraints)
	if err != nil {
		return nil, nil, err
	}
	return actionsJSON, constraintsJSON, nil
}

func formatOptionalTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func capabilityGrantsEqual(left, right capability.Grant) bool {
	leftActions, leftConstraints, leftErr := marshalCapabilityScope(left.Scope)
	rightActions, rightConstraints, rightErr := marshalCapabilityScope(right.Scope)
	if leftErr != nil || rightErr != nil {
		return false
	}
	return left.ID == right.ID && left.Subject == right.Subject && left.TaskID == right.TaskID && left.Kind == right.Kind && left.Scope.Resource == right.Scope.Resource && string(leftActions) == string(rightActions) && string(leftConstraints) == string(rightConstraints) && left.Issuer == right.Issuer && left.IssuedAt.UTC().Equal(right.IssuedAt.UTC()) && left.ExpiresAt.UTC().Equal(right.ExpiresAt.UTC()) && optionalTimesEqual(left.RevokedAt, right.RevokedAt) && left.PolicyDigest == right.PolicyDigest && left.IdempotencyKey == right.IdempotencyKey
}

func optionalTimesEqual(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.UTC().Equal(right.UTC())
}
