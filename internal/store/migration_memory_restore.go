package store

import (
	"context"
	"database/sql"
)

// migrateMemoryRestore is reserved for v98 registration by the integrator.
// The caller owns the migration transaction and version ledger. The DDL is
// idempotent (IF NOT EXISTS) so a rewound ledger that re-runs v98 never
// collides with tables created by an earlier pass.
func migrateMemoryRestore(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
 CREATE TABLE IF NOT EXISTS memory_restore_snapshots(project_id TEXT NOT NULL,snapshot_id TEXT NOT NULL,manifest_digest TEXT NOT NULL,manifest_json BLOB NOT NULL,payload BLOB NOT NULL,created_at TEXT NOT NULL,PRIMARY KEY(project_id,snapshot_id));
 CREATE TRIGGER IF NOT EXISTS memory_restore_snapshot_no_update BEFORE UPDATE ON memory_restore_snapshots BEGIN SELECT RAISE(ABORT,'immutable memory snapshot'); END;
 CREATE TABLE IF NOT EXISTS memory_restore_heads(project_id TEXT PRIMARY KEY,generation INTEGER NOT NULL CHECK(generation>=0));
 CREATE TABLE IF NOT EXISTS memory_restore_history(project_id TEXT NOT NULL,generation INTEGER NOT NULL,snapshot_id TEXT NOT NULL,snapshot_digest TEXT NOT NULL,result_json BLOB NOT NULL,created_at TEXT NOT NULL,PRIMARY KEY(project_id,generation));
 CREATE TRIGGER IF NOT EXISTS memory_restore_history_no_update BEFORE UPDATE ON memory_restore_history BEGIN SELECT RAISE(ABORT,'immutable memory restore history'); END;
 `)
	return err
}

// MigrateMemoryRestore exposes the same canonical DDL for isolated provisional
// worker fixtures. Production must register migrateMemoryRestore in Migrate;
// neither restore services nor Runtime.Open invoke DDL.
func MigrateMemoryRestore(ctx context.Context, tx *sql.Tx) error {
	return migrateMemoryRestore(ctx, tx)
}
