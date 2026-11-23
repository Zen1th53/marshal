package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Zen1th53/marshal/internal/model"
)

const ultraExecutionPreferenceSchema = `CREATE TABLE IF NOT EXISTS ultra_execution_preferences (
 project_id TEXT PRIMARY KEY REFERENCES projects(project_id),
 enabled INTEGER NOT NULL CHECK(enabled IN (0,1))
 )`

func init() {
	registerCanonicalMigration(111, func(ctx context.Context, tx *sql.Tx) error {
		if err := ddlMigration(ultraExecutionPreferenceSchema)(ctx, tx); err != nil {
			return err
		}
		// Preserve preferences written before they had their own record.
		_, err := tx.ExecContext(ctx, `INSERT INTO ultra_execution_preferences(project_id,enabled)
 SELECT project_id,json_extract(data_json,'$.UltraExecution') FROM marshal_settings
 WHERE json_type(data_json,'$.UltraExecution') IN ('true','false')
 ON CONFLICT(project_id) DO NOTHING`)
		return err
	})
}

// GetUltraExecutionPreference returns nil when the project has no preference.
func (s *Store) GetUltraExecutionPreference(ctx context.Context, projectID string) (*bool, error) {
	var enabled bool
	err := s.db.QueryRowContext(ctx, `SELECT enabled FROM ultra_execution_preferences WHERE project_id=?`, projectID).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &enabled, nil
}

// SetUltraExecutionPreference atomically replaces only the execution preference.
// No read revision is required, so an off write cannot lose a revision race
// with another toggle or a project settings update.
func (s *Store) SetUltraExecutionPreference(ctx context.Context, projectID string, enabled bool) error {
	if projectID == "" {
		return fmt.Errorf("%w: missing project", model.ErrInvalid)
	}
	for attempt := 0; ; attempt++ {
		_, err := s.db.ExecContext(ctx, `INSERT INTO ultra_execution_preferences(project_id,enabled) VALUES(?,?)
 ON CONFLICT(project_id) DO UPDATE SET enabled=excluded.enabled`, projectID, enabled)
		if !isSQLiteBusy(err) || attempt >= sqliteBusyRetries {
			return err
		}
		if err := waitSQLiteRetry(ctx, attempt); err != nil {
			return err
		}
	}
}
