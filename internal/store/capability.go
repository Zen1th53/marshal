package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/model"
)

func (s *Store) SaveGrant(ctx context.Context, grant capability.Grant) error {
	if err := grant.Validate(); err != nil {
		return err
	}
	actions, err := json.Marshal(grant.Scope.Actions)
	if err != nil {
		return fmt.Errorf("encode capability actions: %w", err)
	}
	constraints, err := json.Marshal(grant.Scope.Constraints)
	if err != nil {
		return fmt.Errorf("encode capability constraints: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO capability_grants(
			id, subject, task_id, kind, resource, actions_json, constraints_json,
			issuer, issued_at, expires_at, revoked_at, policy_digest, state
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, grant.ID, grant.Subject, grant.TaskID, string(grant.Kind), grant.Scope.Resource,
		string(actions), string(constraints), grant.Issuer, grant.IssuedAt.UTC().Format(time.RFC3339Nano),
		grant.ExpiresAt.UTC().Format(time.RFC3339Nano), nullableTime(grant.RevokedAt), grant.PolicyDigest, string(grant.State))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
			stored, loadErr := s.LoadGrant(ctx, grant.ID)
			if loadErr == nil && reflect.DeepEqual(stored, grant) {
				return nil
			}
			return fmt.Errorf("%w: capability grant identity is immutable", model.ErrConflict)
		}
		return fmt.Errorf("persist capability grant: %w", err)
	}
	return nil
}

func (s *Store) LoadGrant(ctx context.Context, id string) (capability.Grant, error) {
	var grant capability.Grant
	var kind, resource, actions, constraints, issued, expires, revoked, state string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, subject, task_id, kind, resource, actions_json, constraints_json,
		       issuer, issued_at, expires_at, COALESCE(revoked_at, ''), policy_digest, state
		FROM capability_grants WHERE id = ?
	`, id).Scan(&grant.ID, &grant.Subject, &grant.TaskID, &kind, &resource, &actions, &constraints,
		&grant.Issuer, &issued, &expires, &revoked, &grant.PolicyDigest, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return capability.Grant{}, capability.ErrGrantNotFound
	}
	if err != nil {
		return capability.Grant{}, fmt.Errorf("load capability grant: %w", err)
	}
	grant.Kind, grant.Scope.Resource, grant.State = capability.Kind(kind), resource, capability.GrantState(state)
	if err := json.Unmarshal([]byte(actions), &grant.Scope.Actions); err != nil {
		return capability.Grant{}, fmt.Errorf("decode capability actions: %w", err)
	}
	if err := json.Unmarshal([]byte(constraints), &grant.Scope.Constraints); err != nil {
		return capability.Grant{}, fmt.Errorf("decode capability constraints: %w", err)
	}
	grant.IssuedAt, err = time.Parse(time.RFC3339Nano, issued)
	if err != nil {
		return capability.Grant{}, fmt.Errorf("decode capability issued time: %w", err)
	}
	grant.ExpiresAt, err = time.Parse(time.RFC3339Nano, expires)
	if err != nil {
		return capability.Grant{}, fmt.Errorf("decode capability expiry: %w", err)
	}
	if revoked != "" {
		value, parseErr := time.Parse(time.RFC3339Nano, revoked)
		if parseErr != nil {
			return capability.Grant{}, fmt.Errorf("decode capability revocation time: %w", parseErr)
		}
		grant.RevokedAt = &value
	}
	if err := grant.Validate(); err != nil {
		return capability.Grant{}, fmt.Errorf("invalid durable capability grant: %w", err)
	}
	return grant, nil
}

func (s *Store) ListGrants(ctx context.Context, kind capability.Kind) ([]capability.Grant, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM capability_grants WHERE kind = ? ORDER BY id", string(kind))
	if err != nil {
		return nil, fmt.Errorf("list capability grants: %w", err)
	}
	defer rows.Close()
	var grants []capability.Grant
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan capability grant id: %w", err)
		}
		grant, err := s.LoadGrant(ctx, id)
		if err != nil {
			return nil, err
		}
		grants = append(grants, grant)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate capability grants: %w", err)
	}
	return grants, nil
}

func (s *Store) RevokeGrant(ctx context.Context, id string, at time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE capability_grants SET revoked_at = ?, state = 'revoked' WHERE id = ? AND state NOT IN ('revoked','expired')`, at.UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return fmt.Errorf("revoke capability grant: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check capability revocation: %w", err)
	}
	if count != 1 {
		return capability.ErrGrantNotFound
	}
	return nil
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}
