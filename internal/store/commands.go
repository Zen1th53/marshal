package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
)

// CommandRecord contains identifiers and a payload digest, never command prose.
type CommandRecord struct {
	ProjectID, Actor, Key, Operation, SessionID, TargetID, Digest string
	CapabilityGrantID                                             string
	ExpectedVersion, ResultVersion                                int64
}
type commandKey struct{}

func WithCommand(ctx context.Context, r CommandRecord) context.Context {
	return context.WithValue(ctx, commandKey{}, r)
}

func (s *Store) CommandResult(ctx context.Context, r CommandRecord) (int64, bool, error) {
	var digest, operation, session, target string
	var expected, result int64
	err := s.db.QueryRowContext(ctx, `SELECT digest,operation,session_id,target_id,expected_version,result_version FROM command_results WHERE project_id=? AND actor=? AND command_key=?`, r.ProjectID, r.Actor, r.Key).Scan(&digest, &operation, &session, &target, &expected, &result)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if digest != r.Digest || operation != r.Operation || session != r.SessionID || target != r.TargetID || expected != r.ExpectedVersion {
		return 0, false, fmt.Errorf("%w: idempotency key already binds another command", model.ErrConflict)
	}
	return result, true, nil
}

func writeCommand(ctx context.Context, tx *sql.Tx, goal model.GoalContract) error {
	r, ok := ctx.Value(commandKey{}).(CommandRecord)
	if !ok {
		return nil
	}
	if r.ProjectID != goal.ProjectID || r.TargetID != goal.ID || r.SessionID != goal.SessionID || r.ExpectedVersion+1 != goal.Revision {
		return model.ErrConflict
	}
	// Recheck revocation/expiry in the mutation transaction: a grant revoked
	// after application authorization must not permit a later commit.
	if r.CapabilityGrantID != "" {
		var expiry string
		err := tx.QueryRowContext(ctx, `SELECT expires_at FROM capability_grants WHERE id=? AND subject=? AND task_id=? AND revoked_at IS NULL`, r.CapabilityGrantID, r.Actor, r.ProjectID).Scan(&expiry)
		if errors.Is(err, sql.ErrNoRows) {
			return authz.ErrDenied
		}
		if err != nil {
			return err
		}
		expiresAt, err := time.Parse(time.RFC3339Nano, expiry)
		if err != nil || !time.Now().UTC().Before(expiresAt) {
			return authz.ErrDenied
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO command_results(project_id,actor,command_key,operation,session_id,target_id,expected_version,digest,result_version) VALUES(?,?,?,?,?,?,?,?,?)`, r.ProjectID, r.Actor, r.Key, r.Operation, r.SessionID, r.TargetID, r.ExpectedVersion, r.Digest, goal.Revision)
	if err != nil {
		return fmt.Errorf("persist command result: %w", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO command_audit(project_id,actor,operation,target_id,result_version,result,created_at) VALUES(?,?,?,?,?,'applied',?)`, r.ProjectID, r.Actor, r.Operation, r.TargetID, goal.Revision, utcNow())
	if err != nil {
		return fmt.Errorf("persist command audit: %w", err)
	}
	return nil
}
