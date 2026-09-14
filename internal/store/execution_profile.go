package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// GetExecutionModelPreference returns the project-scoped model preference for
// an adapter.  Callers must still validate this preference against a fresh
// harness catalog before dispatch.
func (s *Store) GetExecutionModelPreference(ctx context.Context, projectID, adapterName string) (model.ExecutionModelPreference, error) {
	if strings.TrimSpace(projectID) == "" || strings.TrimSpace(adapterName) == "" {
		return model.ExecutionModelPreference{}, fmt.Errorf("%w: project ID and adapter are required", model.ErrInvalid)
	}
	var preference model.ExecutionModelPreference
	var updatedAt string
	err := s.db.QueryRowContext(ctx, `
		SELECT project_id, adapter, model, revision, updated_at
		FROM execution_model_preferences
		WHERE project_id = ? AND adapter = ?
	`, projectID, adapterName).Scan(&preference.ProjectID, &preference.Adapter, &preference.Model, &preference.Revision, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.ExecutionModelPreference{}, fmt.Errorf("%w: execution model preference for %s", model.ErrNotFound, adapterName)
	}
	if err != nil {
		return model.ExecutionModelPreference{}, fmt.Errorf("read execution model preference: %w", err)
	}
	var parseErr error
	preference.UpdatedAt, parseErr = time.Parse(time.RFC3339Nano, updatedAt)
	if parseErr != nil {
		return model.ExecutionModelPreference{}, fmt.Errorf("parse execution model preference time: %w", parseErr)
	}
	return preference, nil
}

// SetExecutionModelPreference creates or CAS-updates a project-scoped adapter
// preference.  expectedRevision is the exact revision shown to the operator;
// zero creates the first preference and later writes must name the current
// revision.  A conflict is never silently retried because it could replace a
// model another operator selected while the confirmation was open.
func (s *Store) SetExecutionModelPreference(ctx context.Context, preference model.ExecutionModelPreference, expectedRevision int64) (model.ExecutionModelPreference, error) {
	if strings.TrimSpace(preference.ProjectID) == "" || strings.TrimSpace(preference.Adapter) == "" || strings.TrimSpace(preference.Model) == "" {
		return model.ExecutionModelPreference{}, fmt.Errorf("%w: project ID, adapter, and model are required", model.ErrInvalid)
	}
	if expectedRevision < 0 {
		return model.ExecutionModelPreference{}, fmt.Errorf("%w: expected revision must be non-negative", model.ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.ExecutionModelPreference{}, fmt.Errorf("begin execution model preference update: %w", err)
	}
	defer tx.Rollback()

	var currentRevision int64
	err = tx.QueryRowContext(ctx, `
		SELECT revision FROM execution_model_preferences WHERE project_id = ? AND adapter = ?
	`, preference.ProjectID, preference.Adapter).Scan(&currentRevision)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if expectedRevision != 0 {
			return model.ExecutionModelPreference{}, fmt.Errorf("%w: execution model preference does not exist at revision %d", model.ErrConflict, expectedRevision)
		}
		preference.Revision = 1
	case err != nil:
		return model.ExecutionModelPreference{}, fmt.Errorf("read execution model preference revision: %w", err)
	case currentRevision != expectedRevision:
		return model.ExecutionModelPreference{}, fmt.Errorf("%w: execution model preference revision conflict (current=%d, expected=%d)", model.ErrConflict, currentRevision, expectedRevision)
	default:
		preference.Revision = currentRevision + 1
	}
	preference.UpdatedAt = time.Now().UTC()
	if currentRevision == 0 {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO execution_model_preferences(project_id, adapter, model, revision, updated_at)
			VALUES(?, ?, ?, ?, ?)
		`, preference.ProjectID, preference.Adapter, preference.Model, preference.Revision, preference.UpdatedAt.Format(time.RFC3339Nano))
	} else {
		result, updateErr := tx.ExecContext(ctx, `
			UPDATE execution_model_preferences
			SET model = ?, revision = ?, updated_at = ?
			WHERE project_id = ? AND adapter = ? AND revision = ?
		`, preference.Model, preference.Revision, preference.UpdatedAt.Format(time.RFC3339Nano), preference.ProjectID, preference.Adapter, currentRevision)
		if updateErr == nil {
			rows, rowsErr := result.RowsAffected()
			if rowsErr != nil {
				return model.ExecutionModelPreference{}, fmt.Errorf("check execution model preference update: %w", rowsErr)
			}
			if rows != 1 {
				return model.ExecutionModelPreference{}, fmt.Errorf("%w: execution model preference update conflict", model.ErrConflict)
			}
		}
		err = updateErr
	}
	if err != nil {
		return model.ExecutionModelPreference{}, fmt.Errorf("write execution model preference: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return model.ExecutionModelPreference{}, fmt.Errorf("commit execution model preference: %w", err)
	}
	return preference, nil
}
