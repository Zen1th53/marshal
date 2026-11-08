//go:build !governed

package store

import (
	"context"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/model"
)

// RunEventTimeline reads at most twenty retained run events, newest sequence
// first. The exact project/run must already exist. Payloads, subjects, resource
// references and idempotency keys are deliberately never selected or decoded.
func (s *Store) RunEventTimeline(ctx context.Context, projectID, runID string, limit int) ([]events.Event, error) {
	if projectID == "" || runID == "" || limit < 1 || limit > 20 {
		return nil, model.ErrInvalid
	}
	if _, err := s.GetMarshalRun(ctx, projectID, runID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT event_id, sequence, event_type, task_id, run_id, at
		FROM structured_events WHERE run_id = ? AND subject = ? AND EXISTS
		(SELECT 1 FROM marshal_runs WHERE project_id = ? AND run_id = ?)
		ORDER BY sequence DESC LIMIT ?`, runID, projectID, projectID, runID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]events.Event, 0, limit)
	for rows.Next() {
		var event events.Event
		var at string
		if err := rows.Scan(&event.ID, &event.Sequence, &event.Type, &event.TaskID, &event.RunID, &at); err != nil {
			return nil, err
		}
		event.At, err = time.Parse(time.RFC3339Nano, at)
		if err != nil {
			return nil, fmt.Errorf("timeline timestamp: %w", err)
		}
		result = append(result, event)
	}
	return result, rows.Err()
}
