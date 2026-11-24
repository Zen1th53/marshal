package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"time"

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
		err := s.setUltraPreferenceAudited(ctx, projectID, enabled)
		if !isSQLiteBusy(err) || attempt >= sqliteBusyRetries {
			return err
		}
		if err := waitSQLiteRetry(ctx, attempt); err != nil {
			return err
		}
	}
}

func (s *Store) setUltraPreferenceAudited(ctx context.Context, projectID string, enabled bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = s.setUltraPreferenceTx(ctx, tx, projectID, enabled); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *Store) setUltraPreferenceTx(ctx context.Context, tx *sql.Tx, projectID string, enabled bool) error {
	var old bool
	err := tx.QueryRowContext(ctx, "SELECT enabled FROM ultra_execution_preferences WHERE project_id=?", projectID).Scan(&old)
	existed := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO ultra_execution_preferences(project_id,enabled) VALUES(?,?) ON CONFLICT(project_id) DO UPDATE SET enabled=excluded.enabled`, projectID, enabled); err != nil {
		return err
	}
	if !existed || old != enabled {
		var rev int64
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(aggregate_revision),0)+1 FROM audit_events WHERE project_id=? AND event_type='marshal.settings.changed'`, projectID).Scan(&rev); err != nil {
			return err
		}
		before := "default"
		if existed {
			before = fmt.Sprint(old)
		}
		id := uuid.NewString()
		at := time.Now().UTC()
		err = s.appendEventTx(ctx, tx, model.Event{ID: id, Type: "marshal.settings.changed", ProjectID: projectID, AggregateRevision: rev, Timestamp: at, Data: map[string]any{"id": id, "revision": rev, "key": "ultra-execution", "old": before, "new": fmt.Sprint(enabled), "actor": "owner", "at": at, "scope": "project execution preference", "applies": "next admission"}})
		if err != nil {
			return err
		}
	}
	return nil
}
