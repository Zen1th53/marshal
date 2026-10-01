package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
)

// CommandRecord contains identifiers and a payload digest, never command prose.
type CommandRecord struct {
	ProjectID, Actor, Key, Operation, SessionID, TargetID, Digest string
	CapabilityGrantID                                             string
	Result                                                        string
	TargetProjectID                                               string
	ExpectedStateDigest                                           string
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
	return writeDecisionCommand(ctx, tx, goal.ProjectID, goal.SessionID, goal.ID, goal.Revision)
}

func writeDecisionCommand(ctx context.Context, tx *sql.Tx, project, session, target string, version int64) error {
	r, ok := ctx.Value(commandKey{}).(CommandRecord)
	if !ok {
		return nil
	}
	match := r.TargetID == target || r.TargetID == fmt.Sprintf("goal:%s@%d", target, r.ExpectedVersion) || r.TargetID == fmt.Sprintf("plan:%s@%d", target, r.ExpectedVersion) || r.TargetID == "approval:"+target || r.TargetID == "execution:"+target
	if (r.ProjectID != project && r.TargetProjectID != project) || r.SessionID != session || !match || (r.ExpectedVersion+1 != version && !(strings.HasPrefix(r.TargetID, "execution:") && version > r.ExpectedVersion)) {
		return model.ErrConflict
	}
	return insertCommandReceipt(ctx, tx, r, version)
}

// insertCommandReceipt rechecks the grant inside the mutation transaction and
// writes the command result and audit record there, so a mutation and its
// receipt commit or roll back together.
func insertCommandReceipt(ctx context.Context, tx *sql.Tx, r CommandRecord, version int64) error {
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
	_, err := tx.ExecContext(ctx, `INSERT INTO command_results(project_id,actor,command_key,operation,session_id,target_id,expected_version,digest,result_version) VALUES(?,?,?,?,?,?,?,?,?)`, r.ProjectID, r.Actor, r.Key, r.Operation, r.SessionID, r.TargetID, r.ExpectedVersion, r.Digest, version)
	if err != nil {
		return fmt.Errorf("persist command result: %w", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO command_audit(project_id,actor,operation,target_id,result_version,result,created_at) VALUES(?,?,?,?,?,?,?)`, r.ProjectID, r.Actor, r.Operation, r.TargetID, version, commandOutcome(r), utcNow())
	if err != nil {
		return fmt.Errorf("persist command audit: %w", err)
	}
	return nil
}

// PersistDecisionCommand commits a durable execution decision intent or result.
// File-backed execution decisions use the intent as their recovery/outbox key.
func (s *Store) PersistDecisionCommand(ctx context.Context, r CommandRecord, result string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r.Result = result
	ctx = WithCommand(ctx, r)
	version := r.ResultVersion
	if version == 0 {
		version = r.ExpectedVersion + 1
	}
	if err := writeDecisionCommand(ctx, tx, r.ProjectID, r.SessionID, strings.TrimPrefix(r.TargetID, "execution:"), version); err != nil {
		return err
	}

	return tx.Commit()
}

func commandOutcome(r CommandRecord) string {
	if r.Result != "" {
		return r.Result
	}
	return "applied"
}
