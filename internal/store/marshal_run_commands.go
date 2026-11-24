package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Zen1th53/marshal/internal/model"
)

type marshalCommandKey struct{}
type marshalCommandAdmissionKey struct{}

func init() {
	registerCanonicalMigration(113, ddlMigration(`CREATE TABLE IF NOT EXISTS marshal_command_bindings (
 project_id TEXT NOT NULL, actor TEXT NOT NULL, command_id TEXT NOT NULL,
 run_id TEXT NOT NULL, digest TEXT NOT NULL,
 PRIMARY KEY(project_id,actor,command_id))`))
}

// WithMarshalCommand binds a run state mutation to its command receipt.
func WithMarshalCommand(ctx context.Context, record CommandRecord) context.Context {
	return context.WithValue(ctx, marshalCommandKey{}, record)
}

// WithMarshalCommandAdmission reserves the project-wide command identity in
// the same transaction that persists its queued receipt.
func WithMarshalCommandAdmission(ctx context.Context, record CommandRecord) context.Context {
	return context.WithValue(ctx, marshalCommandAdmissionKey{}, record)
}

func bindMarshalCommandTx(ctx context.Context, tx *sql.Tx, record CommandRecord) error {
	var runID, digest string
	err := tx.QueryRowContext(ctx, `SELECT run_id,digest FROM marshal_command_bindings WHERE project_id=? AND actor=? AND command_id=?`, record.ProjectID, record.Actor, record.Key).Scan(&runID, &digest)
	if err == nil {
		if runID != record.SessionID || digest != record.Digest {
			return model.ErrConflict
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO marshal_command_bindings(project_id,actor,command_id,run_id,digest) VALUES(?,?,?,?,?)`, record.ProjectID, record.Actor, record.Key, record.SessionID, record.Digest)
	return err
}
