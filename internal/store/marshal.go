package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

// MarshalRecord carries a stored value and its revision for guarded updates.
type MarshalRecord[T any] struct {
	Value    T
	Revision int64
}

func marshalWrite[T any](ctx context.Context, s *Store, table string, keys []string, values []any, value T, expected int64) (int64, error) {
	if expected < 0 {
		return 0, fmt.Errorf("%w: negative revision", model.ErrInvalid)
	}
	data, err := json.Marshal(value)
	if err != nil {
		return 0, fmt.Errorf("marshal record: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	where := make([]string, len(keys))
	for i, k := range keys {
		where[i] = k + " = ?"
	}
	var current int64
	err = tx.QueryRowContext(ctx, "SELECT revision FROM "+table+" WHERE "+strings.Join(where, " AND "), values...).Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if current != expected {
		return 0, fmt.Errorf("%w: %s revision %d, expected %d", model.ErrConflict, table, current, expected)
	}
	next := current + 1
	if current == 0 {
		cols := append(append([]string{}, keys...), "revision", "data_json")
		args := append(append([]any{}, values...), next, string(data))
		marks := make([]string, len(args))
		for i := range marks {
			marks[i] = "?"
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO "+table+" ("+strings.Join(cols, ",")+") VALUES ("+strings.Join(marks, ",")+")", args...)
	} else {
		args := append([]any{next, string(data)}, values...)
		args = append(args, current)
		result, updateErr := tx.ExecContext(ctx, "UPDATE "+table+" SET revision = ?, data_json = ? WHERE "+strings.Join(where, " AND ")+" AND revision = ?", args...)
		if updateErr != nil {
			err = updateErr
		} else {
			var n int64
			n, err = result.RowsAffected()
			if err == nil && n != 1 {
				err = fmt.Errorf("%w: concurrent %s update", model.ErrConflict, table)
			}
		}
	}
	if err != nil {
		return 0, fmt.Errorf("write %s: %w", table, err)
	}
	if err = tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit %s: %w", table, err)
	}
	return next, nil
}

func marshalInsert[T any](ctx context.Context, s *Store, table string, keys []string, values []any, value T) (int64, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return 0, fmt.Errorf("marshal record: %w", err)
	}
	cols := append(append([]string{}, keys...), "revision", "data_json")
	args := append(append([]any{}, values...), int64(1), string(data))
	marks := make([]string, len(args))
	for i := range marks {
		marks[i] = "?"
	}
	result, err := s.db.ExecContext(ctx, "INSERT INTO "+table+" ("+strings.Join(cols, ",")+") VALUES ("+strings.Join(marks, ",")+") ON CONFLICT DO NOTHING", args...)
	if err != nil {
		return 0, fmt.Errorf("insert %s: %w", table, err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n != 1 {
		return 0, fmt.Errorf("%w: %s attempt already exists", model.ErrConflict, table)
	}
	return 1, nil
}

func marshalRead[T any](ctx context.Context, s *Store, table string, keys []string, values []any) (MarshalRecord[T], error) {
	var out MarshalRecord[T]
	where := make([]string, len(keys))
	for i, k := range keys {
		where[i] = k + " = ?"
	}
	var data string
	err := s.db.QueryRowContext(ctx, "SELECT revision, data_json FROM "+table+" WHERE "+strings.Join(where, " AND "), values...).Scan(&out.Revision, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return out, fmt.Errorf("%w: %s", model.ErrNotFound, table)
	}
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal([]byte(data), &out.Value); err != nil {
		return out, fmt.Errorf("decode %s: %w", table, err)
	}
	return out, nil
}

// SetMarshalRun saves a run only when its revision matches the caller's view.
func (s *Store) SetMarshalRun(ctx context.Context, projectID, runID string, run marshal.Run, expected int64) (int64, error) {
	if projectID == "" || runID == "" || run.PlanID == "" || run.PlanVersion < 1 || run.Settings.Validate() != nil {
		return 0, fmt.Errorf("%w: invalid marshal run", model.ErrInvalid)
	}
	return marshalWrite(ctx, s, "marshal_runs", []string{"run_id", "project_id"}, []any{runID, projectID}, run, expected)
}

// GetMarshalRun loads the durable state needed to resume a run.
func (s *Store) GetMarshalRun(ctx context.Context, projectID, runID string) (MarshalRecord[marshal.Run], error) {
	return marshalRead[marshal.Run](ctx, s, "marshal_runs", []string{"run_id", "project_id"}, []any{runID, projectID})
}

// LatestMarshalRun returns the newest non-closed run for a project, falling
// back to the newest stored run when all runs are closed.
func (s *Store) LatestMarshalRun(ctx context.Context, projectID string) (string, MarshalRecord[marshal.Run], error) {
	rows, err := s.db.QueryContext(ctx, "SELECT run_id, revision, data_json FROM marshal_runs WHERE project_id = ? ORDER BY rowid DESC", projectID)
	if err != nil {
		return "", MarshalRecord[marshal.Run]{}, err
	}
	defer rows.Close()
	var closedID string
	var closed MarshalRecord[marshal.Run]
	for rows.Next() {
		var runID, data string
		var record MarshalRecord[marshal.Run]
		if err := rows.Scan(&runID, &record.Revision, &data); err != nil {
			return "", MarshalRecord[marshal.Run]{}, err
		}
		if err := json.Unmarshal([]byte(data), &record.Value); err != nil {
			return "", MarshalRecord[marshal.Run]{}, fmt.Errorf("decode marshal run: %w", err)
		}
		if record.Value.State != marshal.Closed {
			return runID, record, nil
		}
		if closedID == "" {
			closedID, closed = runID, record
		}
	}
	if err := rows.Err(); err != nil {
		return "", MarshalRecord[marshal.Run]{}, err
	}
	if closedID == "" {
		return "", MarshalRecord[marshal.Run]{}, fmt.Errorf("%w: marshal_runs", model.ErrNotFound)
	}
	return closedID, closed, nil
}

// SetMarshalTask guards task updates against stale run workers.
func (s *Store) SetMarshalTask(ctx context.Context, runID string, task marshal.Task, expected int64) (int64, error) {
	if runID == "" || task.PlanTaskID == "" {
		return 0, fmt.Errorf("%w: missing task identity", model.ErrInvalid)
	}
	return marshalWrite(ctx, s, "marshal_tasks", []string{"run_id", "task_id"}, []any{runID, task.PlanTaskID}, task, expected)
}

// GetMarshalTask loads the latest stored task state.
func (s *Store) GetMarshalTask(ctx context.Context, runID, taskID string) (MarshalRecord[marshal.Task], error) {
	return marshalRead[marshal.Task](ctx, s, "marshal_tasks", []string{"run_id", "task_id"}, []any{runID, taskID})
}

// SetMarshalHandIn records evidence once per attempt so later writes cannot replace it.
func (s *Store) SetMarshalHandIn(ctx context.Context, runID, taskID string, attempt int, handin marshal.HandIn) (int64, error) {
	if runID == "" || taskID == "" || attempt < 1 {
		return 0, fmt.Errorf("%w: invalid hand-in identity", model.ErrInvalid)
	}
	return marshalInsert(ctx, s, "marshal_handins", []string{"run_id", "task_id", "attempt"}, []any{runID, taskID, attempt}, handin)
}

// GetMarshalHandIn retrieves the evidence recorded for an attempt.
func (s *Store) GetMarshalHandIn(ctx context.Context, runID, taskID string, attempt int) (MarshalRecord[marshal.HandIn], error) {
	return marshalRead[marshal.HandIn](ctx, s, "marshal_handins", []string{"run_id", "task_id", "attempt"}, []any{runID, taskID, attempt})
}

// SetMarshalReview records a decision once per attempt so it cannot be revised in place.
func (s *Store) SetMarshalReview(ctx context.Context, runID, taskID string, attempt int, review marshal.Review) (int64, error) {
	if runID == "" || taskID == "" || attempt < 1 {
		return 0, fmt.Errorf("%w: invalid review identity", model.ErrInvalid)
	}
	return marshalInsert(ctx, s, "marshal_reviews", []string{"run_id", "task_id", "attempt"}, []any{runID, taskID, attempt}, review)
}

// GetMarshalReview retrieves the decision recorded for an attempt.
func (s *Store) GetMarshalReview(ctx context.Context, runID, taskID string, attempt int) (MarshalRecord[marshal.Review], error) {
	return marshalRead[marshal.Review](ctx, s, "marshal_reviews", []string{"run_id", "task_id", "attempt"}, []any{runID, taskID, attempt})
}

// GetMarshalSettings returns project settings or defaults before the first write.
func (s *Store) GetMarshalSettings(ctx context.Context, projectID string) (MarshalRecord[marshal.Settings], error) {
	if projectID == "" {
		return MarshalRecord[marshal.Settings]{}, fmt.Errorf("%w: missing project", model.ErrInvalid)
	}
	out, err := marshalRead[marshal.Settings](ctx, s, "marshal_settings", []string{"project_id"}, []any{projectID})
	if errors.Is(err, model.ErrNotFound) {
		return MarshalRecord[marshal.Settings]{Value: marshal.DefaultSettings()}, nil
	}
	return out, err
}

// SetMarshalSettings validates and updates project settings by revision.
func (s *Store) SetMarshalSettings(ctx context.Context, projectID string, settings marshal.Settings, expected int64) (int64, error) {
	if projectID == "" {
		return 0, fmt.Errorf("%w: missing project", model.ErrInvalid)
	}
	if err := settings.Validate(); err != nil {
		return 0, fmt.Errorf("%w: %v", model.ErrInvalid, err)
	}
	return marshalWrite(ctx, s, "marshal_settings", []string{"project_id"}, []any{projectID}, settings, expected)
}
