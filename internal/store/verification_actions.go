package store

import (
	"context"
	"encoding/json"
	"github.com/Zen1th53/marshal/internal/verification"
)

// RecordVerificationActionReceipt couples a read-only artifact command to the
// exact canonical verification version that produced it. File existence alone
// cannot stand in for an authorized, current completion envelope.
func (s *Store) RecordVerificationActionReceipt(ctx context.Context, session verification.Session) error {
	command, ok := ctx.Value(commandKey{}).(CommandRecord)
	if !ok {
		return verification.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var body []byte
	if err = tx.QueryRowContext(ctx, `SELECT session_json FROM verification_sessions WHERE verification_id=? AND project_id=? AND version=?`, session.ID, session.Binding.ProjectID, session.Version).Scan(&body); err != nil {
		return err
	}
	expected, err := json.Marshal(session)
	if err != nil {
		return err
	}
	if string(expected) != string(body) {
		return verification.ErrConflict
	}
	if command.ProjectID != session.Binding.ProjectID || command.TargetID != session.ID || command.ExpectedVersion != session.Version {
		return verification.ErrConflict
	}
	if err = insertCommandReceipt(ctx, tx, command, session.Version); err != nil {
		return err
	}
	return tx.Commit()
}
