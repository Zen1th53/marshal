//go:build !governed

package store

import (
	"context"
	"database/sql"
)

func init() {
	registerCanonicalMigration(schemaExtremeApprovedCells, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS extreme_approved_cells (
 cell_id TEXT PRIMARY KEY REFERENCES execution_cells(cell_id),
 project_id TEXT NOT NULL,session_id TEXT NOT NULL,actor TEXT NOT NULL,
 revision INTEGER NOT NULL CHECK(revision>0),descriptor_json TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS extreme_approved_cell_history (
 cell_id TEXT NOT NULL REFERENCES extreme_approved_cells(cell_id),revision INTEGER NOT NULL,
 descriptor_json TEXT NOT NULL,created_at TEXT NOT NULL,PRIMARY KEY(cell_id,revision)
);
CREATE TABLE IF NOT EXISTS extreme_cell_command_phases (
 project_id TEXT NOT NULL,actor TEXT NOT NULL,command_key TEXT NOT NULL,
 revision INTEGER NOT NULL,cell_id TEXT NOT NULL REFERENCES extreme_approved_cells(cell_id),
 PRIMARY KEY(project_id,actor,command_key,revision),
 FOREIGN KEY(project_id,actor,command_key) REFERENCES command_results(project_id,actor,command_key)
);
CREATE TRIGGER IF NOT EXISTS extreme_cell_history_update BEFORE UPDATE ON extreme_approved_cell_history BEGIN SELECT RAISE(ABORT,'cell history is immutable'); END;
CREATE TRIGGER IF NOT EXISTS extreme_cell_history_delete BEFORE DELETE ON extreme_approved_cell_history BEGIN SELECT RAISE(ABORT,'cell history is immutable'); END;
CREATE TRIGGER IF NOT EXISTS extreme_cell_phase_update BEFORE UPDATE ON extreme_cell_command_phases BEGIN SELECT RAISE(ABORT,'cell command phase is immutable'); END;
CREATE TRIGGER IF NOT EXISTS extreme_cell_phase_delete BEFORE DELETE ON extreme_cell_command_phases BEGIN SELECT RAISE(ABORT,'cell command phase is immutable'); END;
`)
		return err
	})
}
