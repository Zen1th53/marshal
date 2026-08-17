package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
)

// Append stores one structured event in the canonical SQLite history. The
// database assigns sequence; callers cannot supply or overwrite it.
func (s *Store) Append(ctx context.Context, event events.Event) (events.Event, error) {
	if err := event.Validate(); err != nil {
		return events.Event{}, err
	}
	if event.ID == "" || event.At.IsZero() {
		return events.Event{}, events.NewError(events.CodeEventTypeInvalid, fmt.Errorf("event identity or timestamp is missing"))
	}
	data, err := json.Marshal(event.Data)
	if err != nil {
		return events.Event{}, events.NewError(events.CodeEventStoreFailed, err)
	}
	var stored events.Event
	var storedAt, storedData string
	var storedIdempotency sql.NullString
	var storedSequence int64
	err = s.db.QueryRowContext(ctx, `
		SELECT sequence, event_type, subject, task_id, run_id, resource_id,
		       evidence_id, at, data_json, idempotency_key
		FROM structured_events WHERE event_id = ?`, event.ID).
		Scan(&storedSequence, &stored.Type, &stored.Subject, &stored.TaskID, &stored.RunID,
			&stored.ResourceID, &stored.EvidenceID, &storedAt, &storedData, &storedIdempotency)
	if err == nil {
		stored.IdempotencyKey = storedIdempotency.String
		if stored.Type == event.Type && stored.Subject == event.Subject && stored.TaskID == event.TaskID &&
			stored.RunID == event.RunID && stored.ResourceID == event.ResourceID && stored.EvidenceID == event.EvidenceID &&
			storedAt == event.At.UTC().Format(time.RFC3339Nano) && storedData == string(data) &&
			stored.IdempotencyKey == event.IdempotencyKey {
			stored.ID = event.ID
			stored.At, _ = time.Parse(time.RFC3339Nano, storedAt)
			_ = json.Unmarshal([]byte(storedData), &stored.Data)
			stored.Sequence = events.Sequence(storedSequence)
			return stored, nil
		}
		return events.Event{}, events.NewError(events.CodeEventSequenceConflict, fmt.Errorf("event ID already exists"))
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return events.Event{}, events.NewError(events.CodeEventStoreFailed, err)
	}

	result, err := s.db.ExecContext(ctx, `
		INSERT INTO structured_events(
			event_id, event_type, subject, task_id, run_id, resource_id,
			evidence_id, at, data_json, idempotency_key
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''))`,
		event.ID, event.Type, event.Subject, event.TaskID, event.RunID, event.ResourceID,
		event.EvidenceID, event.At.UTC().Format(time.RFC3339Nano), string(data), event.IdempotencyKey)
	if err != nil {
		return events.Event{}, events.NewError(events.CodeEventStoreFailed, err)
	}
	sequence, err := result.LastInsertId()
	if err != nil {
		return events.Event{}, events.NewError(events.CodeEventStoreFailed, err)
	}
	event.Sequence = events.Sequence(sequence)
	event.At = event.At.UTC()
	return event, nil
}

// Since returns durable events strictly after after, ordered by sequence.
func (s *Store) Since(ctx context.Context, after events.Sequence) ([]events.Event, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT event_id, sequence, event_type, subject, task_id, run_id,
		       resource_id, evidence_id, at, data_json, idempotency_key
		FROM structured_events WHERE sequence > ? ORDER BY sequence ASC`, int64(after))
	if err != nil {
		return nil, events.NewError(events.CodeEventStoreFailed, err)
	}
	defer rows.Close()
	var result []events.Event
	for rows.Next() {
		var event events.Event
		var at, data string
		var idempotency sql.NullString
		var sequence int64
		if err := rows.Scan(&event.ID, &sequence, &event.Type, &event.Subject, &event.TaskID, &event.RunID,
			&event.ResourceID, &event.EvidenceID, &at, &data, &idempotency); err != nil {
			return nil, events.NewError(events.CodeEventStoreFailed, err)
		}
		event.IdempotencyKey = idempotency.String
		event.Sequence = events.Sequence(sequence)
		event.At, err = time.Parse(time.RFC3339Nano, at)
		if err != nil {
			return nil, events.NewError(events.CodeEventStoreFailed, err)
		}
		if err := json.Unmarshal([]byte(data), &event.Data); err != nil {
			return nil, events.NewError(events.CodeEventStoreFailed, err)
		}
		result = append(result, event)
	}
	if err := rows.Err(); err != nil {
		return nil, events.NewError(events.CodeEventStoreFailed, err)
	}
	return result, nil
}
