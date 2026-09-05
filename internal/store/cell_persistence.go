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

	for attempt := 0; ; attempt++ {
		err := s.putCellTx(ctx, record)
		if !isSQLiteBusy(err) || attempt >= sqliteBusyRetries {
			if err != nil && errors.Is(err, model.ErrConflict) {
				return err
			}
			if err != nil && !errors.Is(err, model.ErrUnavailable) && !errors.Is(err, model.ErrInvalid) {
				return fmt.Errorf("%w: execution cell persistence unavailable: %v", model.ErrUnavailable, err)
			}
			return err
		}
		s.observeContention()
		if err := waitSQLiteRetry(ctx, attempt); err != nil {
			return err
		}
	}
}

func (s *Store) putCellTx(ctx context.Context, record cell.Record) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
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
		if parseErr != nil || !sameCellIdentity(existing, record) {
			return fmt.Errorf("%w: execution cell identity is immutable", model.ErrConflict)
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO execution_cells(cell_id, task_id, backend, workspace, spec_digest, state, process_ref,
			created_at, updated_at, destroyed_at, failure_reason)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, string(record.ID), string(record.TaskID), string(record.Backend), record.Workspace,
		string(record.SpecDigest), string(record.State), string(record.ProcessRef),
		record.CreatedAt.Format(time.RFC3339Nano), record.UpdatedAt.Format(time.RFC3339Nano),
		formatCellTime(record.DestroyedAt), string(record.FailureReason)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (s *Store) ClaimCellPreparation(ctx context.Context, id cell.CellID) (bool, error) {
	if id == "" {
		return false, fmt.Errorf("%w: execution cell id is required", model.ErrInvalid)
	}
	for attempt := 0; ; attempt++ {
		claimed, err := s.claimCellPreparationTx(ctx, id)
		if !isSQLiteBusy(err) || attempt >= sqliteBusyRetries {
			if err != nil && !errors.Is(err, model.ErrUnavailable) && !errors.Is(err, model.ErrInvalid) {
				return false, fmt.Errorf("%w: execution cell claim unavailable: %v", model.ErrUnavailable, err)
			}
			return claimed, err
		}
		s.observeContention()
		if err := waitSQLiteRetry(ctx, attempt); err != nil {
			return false, err
		}
	}
}

func (s *Store) claimCellPreparationTx(ctx context.Context, id cell.CellID) (bool, error) {
	result, err := s.db.ExecContext(ctx, `
		UPDATE execution_cells SET state = 'preparing', updated_at = ?
		WHERE cell_id = ? AND state = 'new'
	`, utcNow(), string(id))
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows == 1, nil
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
	for attempt := 0; ; attempt++ {
		err := s.transitionCellStateTx(ctx, id, from, to)
		if !isSQLiteBusy(err) || attempt >= sqliteBusyRetries {
			if err != nil && !errors.Is(err, model.ErrUnavailable) && !errors.Is(err, model.ErrConflict) && !errors.Is(err, model.ErrInvalid) {
				return fmt.Errorf("%w: execution cell transition unavailable: %v", model.ErrUnavailable, err)
			}
			return err
		}
		s.observeContention()
		if err := waitSQLiteRetry(ctx, attempt); err != nil {
			return err
		}
	}
}

func (s *Store) transitionCellStateTx(ctx context.Context, id cell.CellID, from, to cell.State) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE execution_cells SET state = ?, updated_at = ?
		WHERE cell_id = ? AND state = ?
	`, string(to), utcNow(), string(id), string(from))
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
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

func sameCellIdentity(left, right cell.Record) bool {
	return left.ID == right.ID && left.TaskID == right.TaskID && left.Backend == right.Backend &&
		left.Workspace == right.Workspace && left.SpecDigest == right.SpecDigest
}

var _ cell.Repository = (*Store)(nil)
