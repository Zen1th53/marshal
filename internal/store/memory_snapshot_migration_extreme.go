//go:build !governed

package store

import (
	"context"
	"database/sql"
)

func init() { registerCanonicalMigration(schemaExtremeMemorySnapshots, migrateMemorySnapshots) }

func migrateMemorySnapshots(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS extreme_memory_snapshots (
 project_id TEXT NOT NULL, snapshot_id TEXT NOT NULL,
 version INTEGER NOT NULL CHECK(version=1), session_id TEXT NOT NULL, actor TEXT NOT NULL,
 digest TEXT NOT NULL, snapshot_json TEXT NOT NULL, created_at TEXT NOT NULL,
 PRIMARY KEY(project_id,snapshot_id,version)
);
CREATE TABLE IF NOT EXISTS extreme_memory_restore_previews (
 project_id TEXT NOT NULL, preview_id TEXT NOT NULL,
 snapshot_id TEXT NOT NULL, version INTEGER NOT NULL, session_id TEXT NOT NULL, actor TEXT NOT NULL,
 command_key TEXT NOT NULL, preview_json TEXT NOT NULL, consumed_at TEXT,
 PRIMARY KEY(project_id,preview_id),
 FOREIGN KEY(project_id,snapshot_id,version) REFERENCES extreme_memory_snapshots(project_id,snapshot_id,version)
);
CREATE INDEX IF NOT EXISTS extreme_memory_preview_command ON extreme_memory_restore_previews(project_id,actor,command_key);
CREATE TABLE IF NOT EXISTS extreme_memory_restore_history (
 project_id TEXT NOT NULL, preview_id TEXT NOT NULL, memory_id TEXT NOT NULL,
 before_json TEXT NOT NULL, after_json TEXT NOT NULL, restored_at TEXT NOT NULL,
 PRIMARY KEY(project_id,preview_id,memory_id),
 FOREIGN KEY(project_id,preview_id) REFERENCES extreme_memory_restore_previews(project_id,preview_id)
);
CREATE TRIGGER IF NOT EXISTS extreme_memory_snapshot_immutable_update BEFORE UPDATE ON extreme_memory_snapshots BEGIN SELECT RAISE(ABORT,'memory snapshot is immutable'); END;
CREATE TRIGGER IF NOT EXISTS extreme_memory_snapshot_immutable_delete BEFORE DELETE ON extreme_memory_snapshots BEGIN SELECT RAISE(ABORT,'memory snapshot is immutable'); END;
CREATE TRIGGER IF NOT EXISTS extreme_memory_preview_immutable_update BEFORE UPDATE ON extreme_memory_restore_previews
WHEN NEW.project_id IS NOT OLD.project_id OR NEW.preview_id IS NOT OLD.preview_id
 OR NEW.snapshot_id IS NOT OLD.snapshot_id OR NEW.version IS NOT OLD.version
 OR NEW.session_id IS NOT OLD.session_id OR NEW.actor IS NOT OLD.actor
 OR NEW.command_key IS NOT OLD.command_key OR NEW.preview_json IS NOT OLD.preview_json
 OR OLD.consumed_at IS NOT NULL OR NEW.consumed_at IS NULL
BEGIN SELECT RAISE(ABORT,'memory restore preview is immutable'); END;
CREATE TRIGGER IF NOT EXISTS extreme_memory_preview_immutable_delete BEFORE DELETE ON extreme_memory_restore_previews BEGIN SELECT RAISE(ABORT,'memory restore preview is immutable'); END;
CREATE TRIGGER IF NOT EXISTS extreme_memory_restore_history_immutable_update BEFORE UPDATE ON extreme_memory_restore_history BEGIN SELECT RAISE(ABORT,'memory restore history is immutable'); END;
CREATE TRIGGER IF NOT EXISTS extreme_memory_restore_history_immutable_delete BEFORE DELETE ON extreme_memory_restore_history BEGIN SELECT RAISE(ABORT,'memory restore history is immutable'); END;
`)
	return err
}
