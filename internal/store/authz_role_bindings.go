package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
)

func (s *Store) PutRoleBinding(ctx context.Context, binding authz.RoleBinding) error {
	if err := binding.Validate(); err != nil {
		return err
	}
	err := func() error {
		_, err := s.db.ExecContext(ctx, `INSERT INTO role_bindings
			(binding_id, principal_id, role, scope_id, bound_by, bound_at, revoked_at, policy_digest)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?)`, binding.ID, binding.PrincipalID, binding.Role, binding.ScopeID,
			binding.BoundBy, binding.BoundAt.UTC().Format(time.RFC3339Nano), optionalRoleTime(binding.RevokedAt), binding.PolicyDigest)
		return err
	}()
	if err == nil {
		return nil
	}
	existing, getErr := s.GetRoleBinding(ctx, binding.ID)
	if getErr == nil {
		if existing == binding {
			return nil
		}
		return fmt.Errorf("%w: role binding is immutable", model.ErrConflict)
	}
	return fmt.Errorf("persist role binding: %w", err)
}

func (s *Store) GetRoleBinding(ctx context.Context, id string) (authz.RoleBinding, error) {
	var b authz.RoleBinding
	var boundAt, revokedAt string
	err := s.db.QueryRowContext(ctx, `SELECT binding_id, principal_id, role, scope_id, bound_by, bound_at, COALESCE(revoked_at, ''), policy_digest FROM role_bindings WHERE binding_id = ?`, id).
		Scan(&b.ID, &b.PrincipalID, &b.Role, &b.ScopeID, &b.BoundBy, &boundAt, &revokedAt, &b.PolicyDigest)
	if errorsIsNoRows(err) {
		return authz.RoleBinding{}, fmt.Errorf("%w: role binding not found", model.ErrNotFound)
	}
	if err != nil {
		return authz.RoleBinding{}, fmt.Errorf("read role binding: %w", err)
	}
	var parseErr error
	b.BoundAt, parseErr = time.Parse(time.RFC3339Nano, boundAt)
	if parseErr != nil {
		return authz.RoleBinding{}, fmt.Errorf("%w: invalid role binding time", model.ErrInvalid)
	}
	if revokedAt != "" {
		value, err := time.Parse(time.RFC3339Nano, revokedAt)
		if err != nil {
			return authz.RoleBinding{}, fmt.Errorf("%w: invalid role revoke time", model.ErrInvalid)
		}
		b.RevokedAt = &value
	}
	if err := b.Validate(); err != nil {
		return authz.RoleBinding{}, fmt.Errorf("%w: invalid durable role binding", model.ErrInvalid)
	}
	return b, nil
}

func (s *Store) RevokeRoleBinding(ctx context.Context, id string, revokedAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin role binding revoke: %w", err)
	}
	defer tx.Rollback()
	var binding authz.RoleBinding
	var boundAt, existingRevokedAt string
	err = tx.QueryRowContext(ctx, `
		SELECT binding_id, principal_id, role, scope_id, bound_by, bound_at,
		       COALESCE(revoked_at, ''), policy_digest
		FROM role_bindings WHERE binding_id = ?
	`, id).Scan(&binding.ID, &binding.PrincipalID, &binding.Role, &binding.ScopeID,
		&binding.BoundBy, &boundAt, &existingRevokedAt, &binding.PolicyDigest)
	if errorsIsNoRows(err) {
		return fmt.Errorf("%w: role binding not found", model.ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("read role binding for revoke: %w", err)
	}
	binding.BoundAt, err = time.Parse(time.RFC3339Nano, boundAt)
	if err != nil {
		return fmt.Errorf("%w: invalid role binding time", model.ErrInvalid)
	}
	if !revokedAt.After(binding.BoundAt) {
		return fmt.Errorf("%w: revoke time is invalid", model.ErrInvalid)
	}
	if existingRevokedAt != "" {
		return fmt.Errorf("%w: role binding already revoked", model.ErrConflict)
	}
	result, err := tx.ExecContext(ctx, `UPDATE role_bindings SET revoked_at = ? WHERE binding_id = ? AND revoked_at IS NULL`, revokedAt.UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return fmt.Errorf("revoke role binding: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("%w: role binding already revoked", model.ErrConflict)
	}
	if err := appendTaskMemoryEventTx(ctx, tx, binding.ScopeID, "GRANT_REVOKED", "CRITICAL", "", revokedAt); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit role binding revoke: %w", err)
	}
	return nil
}

func (s *Store) HasActiveRoleBinding(ctx context.Context, principalID, scopeID string) (bool, error) {
	if principalID == "" || scopeID == "" {
		return false, fmt.Errorf("%w: principal and scope are required", model.ErrInvalid)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM role_bindings
		WHERE principal_id = ? AND scope_id = ? AND revoked_at IS NULL
	`, principalID, scopeID).Scan(&count); err != nil {
		return false, fmt.Errorf("query active role binding: %w", err)
	}
	return count > 0, nil
}

func (s *Store) HasActiveTaskSession(ctx context.Context, principalID, taskID string) (bool, error) {
	if principalID == "" || taskID == "" {
		return false, fmt.Errorf("%w: principal and task are required", model.ErrInvalid)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM sessions
		WHERE agent_id = ? AND task_id = ? AND status = 'active'
	`, principalID, taskID).Scan(&count); err != nil {
		return false, fmt.Errorf("query active task session: %w", err)
	}
	return count > 0, nil
}

func optionalRoleTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}
func errorsIsNoRows(err error) bool { return err == sql.ErrNoRows }
