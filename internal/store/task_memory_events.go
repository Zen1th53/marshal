package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

const (
	MaxTaskMemoryEventPage    = 200
	MaxTaskMemoryEventHistory = 4096
)

var ErrTaskMemoryCursorExpired = errors.New("task memory cursor expired; canonical resync required")

type TaskMemoryEventRecord struct {
	TaskID    string
	Sequence  int64
	ProjectID string
	EventType string
	Priority  string
	MemoryID  string
	CreatedAt time.Time
}

// ListTaskMemoryEvents returns a bounded task-local change page. Events are
// notifications only; callers must reload MemoryID from canonical memory.
func (s *Store) ListTaskMemoryEvents(ctx context.Context, projectID, taskID string, after int64, limit int) ([]TaskMemoryEventRecord, int64, bool, error) {
	if projectID == "" || taskID == "" || after < 0 || limit <= 0 || limit > MaxTaskMemoryEventPage {
		return nil, after, false, fmt.Errorf("%w: invalid task event query", model.ErrInvalid)
	}
	var oldest sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `
		SELECT MIN(sequence) FROM task_memory_events WHERE project_id = ? AND task_id = ?
	`, projectID, taskID).Scan(&oldest); err != nil {
		return nil, after, false, fmt.Errorf("inspect task memory cursor window: %w", err)
	}
	if after > 0 && oldest.Valid && after < oldest.Int64-1 {
		return nil, after, false, ErrTaskMemoryCursorExpired
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT task_id, sequence, project_id, event_type, priority, memory_id, created_at
		FROM task_memory_events
		WHERE project_id = ? AND task_id = ? AND sequence > ?
		ORDER BY sequence
		LIMIT ?
	`, projectID, taskID, after, limit+1)
	if err != nil {
		return nil, after, false, fmt.Errorf("list task memory events: %w", err)
	}
	defer rows.Close()

	result := make([]TaskMemoryEventRecord, 0, limit)
	for rows.Next() {
		var event TaskMemoryEventRecord
		var createdAt string
		if err := rows.Scan(&event.TaskID, &event.Sequence, &event.ProjectID, &event.EventType, &event.Priority, &event.MemoryID, &createdAt); err != nil {
			return nil, after, false, fmt.Errorf("scan task memory event: %w", err)
		}
		parsed, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, after, false, fmt.Errorf("%w: invalid task event timestamp", model.ErrInvalid)
		}
		event.CreatedAt = parsed
		result = append(result, event)
	}
	if err := rows.Err(); err != nil {
		return nil, after, false, fmt.Errorf("iterate task memory events: %w", err)
	}
	hasMore := len(result) > limit
	if hasMore {
		result = result[:limit]
	}
	next := after
	if len(result) != 0 {
		next = result[len(result)-1].Sequence
	}
	return result, next, hasMore, nil
}

func appendTaskMemoryEventTx(ctx context.Context, tx *sql.Tx, taskID, eventType, priority, memoryID string, createdAt time.Time) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE task_memory_event_heads SET latest_seq = latest_seq + 1 WHERE task_id = ?
	`, taskID)
	if err != nil {
		return fmt.Errorf("advance task memory event cursor: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		// Registered tasks have a project identity even before their first
		// shared-memory write. Non-task role bindings intentionally produce no
		// task-memory notification.
		result, err = tx.ExecContext(ctx, `
			INSERT INTO task_memory_event_heads(task_id, project_id, latest_seq)
			SELECT task_id, project_id, 1 FROM tasks WHERE task_id = ?
			ON CONFLICT(task_id) DO UPDATE SET latest_seq = latest_seq + 1
		`, taskID)
		if err != nil {
			return fmt.Errorf("initialize task memory event cursor: %w", err)
		}
		rows, err = result.RowsAffected()
		if err != nil {
			return err
		}
		if rows == 0 {
			return nil
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO task_memory_events(task_id, sequence, project_id, event_type, priority, memory_id, created_at)
		SELECT task_id, latest_seq, project_id, ?, ?, ?, ?
		FROM task_memory_event_heads WHERE task_id = ?
	`, eventType, priority, memoryID, createdAt.UTC().Format(time.RFC3339Nano), taskID); err != nil {
		return fmt.Errorf("append task memory event: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM task_memory_events
		WHERE task_id = ? AND sequence <= (
			SELECT latest_seq - ? FROM task_memory_event_heads WHERE task_id = ?
		)
	`, taskID, MaxTaskMemoryEventHistory, taskID); err != nil {
		return fmt.Errorf("prune task memory event history: %w", err)
	}
	return nil
}
