package store

import (
	"context"
	"database/sql"
)

// migrateSessionRecovery is registered by the integrator after memory restore
// (reserved v99). The caller owns the migration transaction/version receipt.
// No runtime path invokes DDL or independently commits this helper. The DDL is
// idempotent so a rewound ledger that re-runs v99 never collides.
func migrateSessionRecovery(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS session_recovery_revisions (
 recovery_id TEXT NOT NULL,
 generation INTEGER NOT NULL CHECK(generation > 0),
 project_id TEXT NOT NULL,
 source_session_id TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('recoverable','closed','adopted')),
 digest TEXT NOT NULL,
 payload TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 PRIMARY KEY(recovery_id,generation)
);
CREATE TABLE IF NOT EXISTS session_recovery_heads (
 recovery_id TEXT PRIMARY KEY,
 generation INTEGER NOT NULL,
 FOREIGN KEY(recovery_id,generation) REFERENCES session_recovery_revisions(recovery_id,generation)
);
CREATE INDEX IF NOT EXISTS session_recovery_by_project ON session_recovery_revisions(project_id,source_session_id,generation);
`)
	return err
}
