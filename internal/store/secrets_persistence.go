package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/secrets"
)

func (s *Store) PutSecretLease(ctx context.Context, lease secrets.Lease) error {
	if err := lease.Validate(); err != nil {
		return fmt.Errorf("%w: invalid secret lease", model.ErrInvalid)
	}
	if lease.IssuedAt.Location() == nil || lease.ExpiresAt.Location() == nil {
		return fmt.Errorf("%w: secret lease time has no location", model.ErrInvalid)
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM tasks WHERE task_id = ?", lease.TaskID).Scan(&exists); err != nil {
		return fmt.Errorf("check secret lease task: %w", err)
	}
	if exists != 1 {
		return fmt.Errorf("%w: secret lease task does not exist", model.ErrInvalid)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO secret_leases(lease_id, subject, task_id, provider, secret_name, secret_version, purpose, issued_at, expires_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, lease.ID, lease.Subject, lease.TaskID, lease.Ref.Provider, lease.Ref.Name, lease.Ref.Version, lease.Purpose,
		lease.IssuedAt.UTC().Format(time.RFC3339Nano), lease.ExpiresAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("persist secret lease: %w", err)
	}
	return nil
}

func (s *Store) GetSecretLease(ctx context.Context, id string) (secrets.Lease, error) {
	if id == "" {
		return secrets.Lease{}, fmt.Errorf("%w: secret lease id is required", model.ErrInvalid)
	}
	var lease secrets.Lease
	var issuedAt, expiresAt, revokedAt, state, accessOwner, accessClaimedAt string
	err := s.db.QueryRowContext(ctx, `
		SELECT lease_id, subject, task_id, provider, secret_name, secret_version, purpose, issued_at, expires_at, COALESCE(revoked_at, ''), state, access_owner, access_claimed_at
		FROM secret_leases WHERE lease_id = ?
	`, id).Scan(&lease.ID, &lease.Subject, &lease.TaskID, &lease.Ref.Provider, &lease.Ref.Name, &lease.Ref.Version, &lease.Purpose, &issuedAt, &expiresAt, &revokedAt, &state, &accessOwner, &accessClaimedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return secrets.Lease{}, fmt.Errorf("%w: secret lease", model.ErrNotFound)
	}
	if err != nil {
		return secrets.Lease{}, fmt.Errorf("read secret lease: %w", err)
	}
	lease.IssuedAt, err = time.Parse(time.RFC3339Nano, issuedAt)
	if err != nil {
		return secrets.Lease{}, fmt.Errorf("%w: corrupt secret lease issued time", model.ErrInvalid)
	}
	lease.ExpiresAt, err = time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil {
		return secrets.Lease{}, fmt.Errorf("%w: corrupt secret lease expiry", model.ErrInvalid)
	}
	lease.State = secrets.LeaseState(state)
	lease.AccessOwner = accessOwner
	if accessClaimedAt != "" {
		claimedAt, parseErr := time.Parse(time.RFC3339Nano, accessClaimedAt)
		if parseErr != nil {
			return secrets.Lease{}, fmt.Errorf("%w: corrupt secret lease claim", model.ErrInvalid)
		}
		lease.AccessClaimedAt = &claimedAt
	}
	if revokedAt != "" {
		revoked, parseErr := time.Parse(time.RFC3339Nano, revokedAt)
		if parseErr != nil {
			return secrets.Lease{}, fmt.Errorf("%w: corrupt secret lease revocation", model.ErrInvalid)
		}
		lease.RevokedAt = &revoked
	}
	return lease, nil
}

func (s *Store) ClaimSecretLease(ctx context.Context, id, owner string, at time.Time) (secrets.Lease, error) {
	if id == "" || owner == "" || at.IsZero() {
		return secrets.Lease{}, fmt.Errorf("%w: invalid secret lease claim", model.ErrInvalid)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE secret_leases SET access_owner = ?, access_claimed_at = ? WHERE lease_id = ? AND state = ? AND (access_owner = '' OR access_claimed_at < ?)`, owner, at.UTC().Format(time.RFC3339Nano), id, secrets.StateLeased, at.Add(-secrets.ClaimTTL).UTC().Format(time.RFC3339Nano))
	if err != nil {
		return secrets.Lease{}, fmt.Errorf("claim secret lease: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return secrets.Lease{}, fmt.Errorf("%w: secret lease already claimed", model.ErrConflict)
	}
	return s.GetSecretLease(ctx, id)
}

func (s *Store) CompleteSecretLease(ctx context.Context, id, owner string, at time.Time) (secrets.Lease, error) {
	if id == "" || owner == "" || at.IsZero() {
		return secrets.Lease{}, fmt.Errorf("%w: invalid secret lease completion", model.ErrInvalid)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE secret_leases SET state = ?, access_owner = '', access_claimed_at = '' WHERE lease_id = ? AND state = ? AND access_owner = ?`, secrets.StateUsed, id, secrets.StateLeased, owner)
	if err != nil {
		return secrets.Lease{}, fmt.Errorf("complete secret lease: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return secrets.Lease{}, fmt.Errorf("%w: stale secret lease owner", model.ErrConflict)
	}
	return s.GetSecretLease(ctx, id)
}

func (s *Store) ReleaseSecretLeaseClaim(ctx context.Context, id, owner string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE secret_leases SET access_owner = '', access_claimed_at = '' WHERE lease_id = ? AND state = ? AND access_owner = ?`, id, secrets.StateLeased, owner)
	if err != nil {
		return fmt.Errorf("release secret lease claim: %w", err)
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows > 1 {
		return fmt.Errorf("%w: invalid secret lease claim release", model.ErrConflict)
	}
	return nil
}

func (s *Store) TransitionSecretLease(ctx context.Context, id string, from, to secrets.LeaseState, at time.Time) (secrets.Lease, error) {
	if id == "" || at.IsZero() || !validSecretLeaseTransition(from, to) {
		return secrets.Lease{}, fmt.Errorf("%w: invalid secret lease transition", model.ErrInvalid)
	}
	revokedAt := ""
	if to == secrets.StateRevoked {
		revokedAt = at.UTC().Format(time.RFC3339Nano)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE secret_leases SET state = ?, revoked_at = NULLIF(?, '') WHERE lease_id = ? AND state = ?`, to, revokedAt, id, from)
	if err != nil {
		return secrets.Lease{}, fmt.Errorf("transition secret lease: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return secrets.Lease{}, fmt.Errorf("%w: secret lease transition lost race", model.ErrConflict)
	}
	return s.GetSecretLease(ctx, id)
}

func validSecretLeaseTransition(from, to secrets.LeaseState) bool {
	switch {
	case from == secrets.StateRequested && to == secrets.StateLeased:
		return true
	case from == secrets.StateLeased && (to == secrets.StateUsed || to == secrets.StateRevoked || to == secrets.StateExpired):
		return true
	default:
		return false
	}
}
