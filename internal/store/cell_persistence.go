package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/cell"
	"github.com/Zen1th53/marshal/internal/model"
)

func (s *Store) PutCell(ctx context.Context, record cell.Record) error {
	if err := record.Validate(); err != nil {
		return fmt.Errorf("%w: invalid execution cell", model.ErrInvalid)
	}
	now := time.Now().UTC()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = record.CreatedAt
	}
	if err := s.ensureCellTimes(record); err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("%w: execution cell persistence unavailable", model.ErrUnavailable)
	}
	defer tx.Rollback()
	var existing cell.Record
	var created, updated, destroyed string
	err = tx.QueryRowContext(ctx, `
		SELECT cell_id, task_id, backend, workspace, spec_digest, state, process_ref,
		       created_at, updated_at, COALESCE(destroyed_at, ''), failure_reason
		FROM execution_cells WHERE cell_id = ?
	`, string(record.ID)).Scan(
		&existing.ID, &existing.TaskID, &existing.Backend, &existing.Workspace,
		&existing.SpecDigest, &existing.State, &existing.ProcessRef,
		&created, &updated, &destroyed, &existing.FailureReason,
	)
	if err == nil {
		parseErr := parseCellTimes(&existing, created, updated, destroyed)
		if parseErr != nil || existing != record {
			return fmt.Errorf("%w: execution cell identity is immutable", model.ErrConflict)
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: execution cell lookup unavailable", model.ErrUnavailable)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO execution_cells(cell_id, task_id, backend, workspace, spec_digest, state, process_ref,
			created_at, updated_at, destroyed_at, failure_reason)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, string(record.ID), string(record.TaskID), string(record.Backend), record.Workspace,
		string(record.SpecDigest), string(record.State), string(record.ProcessRef),
		record.CreatedAt.Format(time.RFC3339Nano), record.UpdatedAt.Format(time.RFC3339Nano),
		formatCellTime(record.DestroyedAt), string(record.FailureReason)); err != nil {
		return fmt.Errorf("%w: execution cell insert failed", model.ErrUnavailable)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%w: execution cell commit failed", model.ErrUnavailable)
	}
	return nil
}

func (s *Store) GetCell(ctx context.Context, id cell.CellID) (cell.Record, error) {
	if id == "" {
		return cell.Record{}, fmt.Errorf("%w: execution cell id is required", model.ErrInvalid)
	}
	var record cell.Record
	var created, updated, destroyed string
	err := s.db.QueryRowContext(ctx, `
		SELECT cell_id, task_id, backend, workspace, spec_digest, state, process_ref,
		       created_at, updated_at, COALESCE(destroyed_at, ''), failure_reason
		FROM execution_cells WHERE cell_id = ?
	`, string(id)).Scan(
		&record.ID, &record.TaskID, &record.Backend, &record.Workspace,
		&record.SpecDigest, &record.State, &record.ProcessRef,
		&created, &updated, &destroyed, &record.FailureReason,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return cell.Record{}, model.ErrNotFound
	}
	if err != nil {
		return cell.Record{}, fmt.Errorf("%w: execution cell lookup failed", model.ErrUnavailable)
	}
	if err := parseCellTimes(&record, created, updated, destroyed); err != nil {
		return cell.Record{}, fmt.Errorf("%w: corrupt execution cell", model.ErrInvalid)
	}
	if err := record.Validate(); err != nil {
		return cell.Record{}, fmt.Errorf("%w: corrupt execution cell", model.ErrInvalid)
	}
	return record, nil
}

func (s *Store) TransitionCellState(ctx context.Context, id cell.CellID, from, to cell.State) error {
	if id == "" {
		return fmt.Errorf("%w: execution cell id is required", model.ErrInvalid)
	}
	if err := cell.ValidateTransition(from, to); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE execution_cells SET state = ?, updated_at = ?
		WHERE cell_id = ? AND state = ?
	`, string(to), utcNow(), string(id), string(from))
	if err != nil {
		return fmt.Errorf("%w: execution cell transition unavailable", model.ErrUnavailable)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return model.ErrConflict
	}
	return nil
}

func (s *Store) ensureCellTimes(record cell.Record) error {
	if record.CreatedAt.Location() != time.UTC || record.UpdatedAt.Location() != time.UTC {
		return fmt.Errorf("%w: execution cell timestamps must be UTC", model.ErrInvalid)
	}
	return nil
}

func parseCellTimes(record *cell.Record, created, updated, destroyed string) error {
	var err error
	if record.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return err
	}
	if record.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return err
	}
	if destroyed != "" {
		value, parseErr := time.Parse(time.RFC3339Nano, destroyed)
		if parseErr != nil {
			return parseErr
		}
		record.DestroyedAt = &value
	}
	return nil
}

func formatCellTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

var _ cell.Repository = (*Store)(nil)
