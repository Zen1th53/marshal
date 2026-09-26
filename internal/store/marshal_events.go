package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
)

// AppendMarshalDecision writes a run decision to the durable event stream.
func (s *Store) AppendMarshalDecision(ctx context.Context, event events.Event) (events.Event, error) {
	if event.RunID == "" || !strings.HasPrefix(string(event.Type), "marshal.") {
		return events.Event{}, events.ErrEventTypeInvalid
	}
	return s.Append(ctx, event)
}

// MarshalDecisions returns one run's decisions in durable sequence order.
func (s *Store) MarshalDecisions(ctx context.Context, runID string) ([]events.Event, error) {
	if runID == "" {
		return nil, events.ErrEventDataInvalid
	}
	rows, err := s.db.QueryContext(ctx, `SELECT event_id, sequence, event_type, subject, task_id, run_id,
		resource_id, evidence_id, at, data_json, idempotency_key FROM structured_events
		WHERE run_id = ? AND event_type LIKE 'marshal.%' ORDER BY sequence ASC`, runID)
	if err != nil {
		return nil, events.NewError(events.CodeEventStoreFailed, err)
	}
	defer rows.Close()
	var result []events.Event
	for rows.Next() {
		var event events.Event
		var sequence int64
		var at, data string
		var key sql.NullString
		if err := rows.Scan(&event.ID, &sequence, &event.Type, &event.Subject, &event.TaskID, &event.RunID,
			&event.ResourceID, &event.EvidenceID, &at, &data, &key); err != nil {
			return nil, events.NewError(events.CodeEventStoreFailed, err)
		}
		event.Sequence = events.Sequence(sequence)
		event.IdempotencyKey = key.String
		if event.At, err = time.Parse(time.RFC3339Nano, at); err != nil {
			return nil, fmt.Errorf("parse marshal event time: %w", err)
		}
		if err := json.Unmarshal([]byte(data), &event.Data); err != nil {
			return nil, fmt.Errorf("decode marshal event data: %w", err)
		}
		result = append(result, event)
	}
	if err := rows.Err(); err != nil {
		return nil, events.NewError(events.CodeEventStoreFailed, err)
	}
	return result, nil
}
