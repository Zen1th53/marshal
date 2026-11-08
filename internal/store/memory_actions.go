package store

import (
	"context"
	"database/sql"
	"errors"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
	"time"
)

// MemoryCommandResult rechecks the live grant and receipt in one read
// transaction. Revoked/expired authority cannot replay a successful mutation.
func (s *Store) MemoryCommandResult(ctx context.Context, r CommandRecord) (int64, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	if r.CapabilityGrantID == "" {
		return 0, false, authz.ErrDenied
	}
	var expiry string
	err = tx.QueryRowContext(ctx, `SELECT expires_at FROM capability_grants WHERE id=? AND subject=? AND task_id=? AND revoked_at IS NULL`, r.CapabilityGrantID, r.Actor, r.ProjectID).Scan(&expiry)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, authz.ErrDenied
	}
	if err != nil {
		return 0, false, err
	}
	until, err := time.Parse(time.RFC3339Nano, expiry)
	if err != nil || !time.Now().UTC().Before(until) {
		return 0, false, authz.ErrDenied
	}
	var digest, operation, session, target string
	var expected, version int64
	err = tx.QueryRowContext(ctx, `SELECT digest,operation,session_id,target_id,expected_version,result_version FROM command_results WHERE project_id=? AND actor=? AND command_key=?`, r.ProjectID, r.Actor, r.Key).Scan(&digest, &operation, &session, &target, &expected, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if digest != r.Digest || operation != r.Operation || session != r.SessionID || target != r.TargetID || expected != r.ExpectedVersion {
		return 0, false, model.ErrConflict
	}
	return version, true, nil
}
