package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/evidence"
)

type eventDigestPayload struct {
	ID             events.EventID        `json:"id"`
	Type           events.Type           `json:"type"`
	Subject        events.SubjectID      `json:"subject"`
	TaskID         events.TaskID         `json:"task_id,omitempty"`
	RunID          events.RunID          `json:"run_id,omitempty"`
	ResourceID     events.ResourceID     `json:"resource_id,omitempty"`
	EvidenceID     events.EvidenceID     `json:"evidence_id,omitempty"`
	Data           map[string]string     `json:"data,omitempty"`
	IdempotencyKey events.IdempotencyKey `json:"idempotency_key"`
}

func eventContentDigest(event events.Event) (string, []byte, error) {
	payload := eventDigestPayload{
		ID: event.ID, Type: event.Type, Subject: event.Subject,
		TaskID: event.TaskID, RunID: event.RunID, ResourceID: event.ResourceID,
		EvidenceID: event.EvidenceID, Data: events.CloneEvent(event).Data,
		IdempotencyKey: event.IdempotencyKey,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", nil, events.NewError(events.CodeInvalidEvent, err)
	}
	digest := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(digest[:]), body, nil
}

// Append durably stores one canonical event and assigns its monotonic sequence
// and UTC timestamp at the authoritative database boundary. Exact retries by
// idempotency key converge to the already committed event.
func (s *Store) Append(ctx context.Context, event events.Event) (events.Event, error) {
	if err := ctx.Err(); err != nil {
		return events.Event{}, events.NewError(events.CodeStoreFailed, err)
	}
	clean := events.CloneEvent(event)
	clean.Sequence = 0
	clean.At = time.Time{}
	if err := clean.Validate(); err != nil {
		return events.Event{}, err
	}
	sanitized, err := s.sanitizer.SanitizeNode(ctx, evidence.Node{Metadata: clean.Data})
	if err != nil {
		return events.Event{}, events.NewError(events.CodeSecretField, err)
	}
	clean.Data = sanitized.Metadata
	digest, _, err := eventContentDigest(clean)
	if err != nil {
		return events.Event{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return events.Event{}, events.NewError(events.CodeStoreFailed, err)
	}
	defer tx.Rollback()

	if existing, existingDigest, found, err := loadEventByIdempotency(ctx, tx, clean.IdempotencyKey); err != nil {
		return events.Event{}, events.NewError(events.CodeStoreFailed, err)
	} else if found {
		if existingDigest != digest {
			return events.Event{}, events.ErrSequenceConflict
		}
		if err := tx.Commit(); err != nil {
			return events.Event{}, events.NewError(events.CodeStoreFailed, err)
		}
		return existing, nil
	}

	dataJSON, err := json.Marshal(clean.Data)
	if err != nil {
		return events.Event{}, events.NewError(events.CodeInvalidEvent, err)
	}
	at := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `
		INSERT INTO structured_events(
			event_id, event_type, subject, task_id, run_id, resource_id, evidence_id,
			timestamp, data_json, idempotency_key, content_digest
		) VALUES(?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, ?)
	`, clean.ID, clean.Type, clean.Subject, clean.TaskID, clean.RunID, clean.ResourceID, clean.EvidenceID,
		at.Format(time.RFC3339Nano), string(dataJSON), clean.IdempotencyKey, digest)
	if err != nil {
		// A concurrent exact retry may have won the unique idempotency key.
		if existing, existingDigest, found, readErr := loadEventByIdempotency(ctx, tx, clean.IdempotencyKey); readErr == nil && found {
			if existingDigest == digest {
				if commitErr := tx.Commit(); commitErr != nil {
					return events.Event{}, events.NewError(events.CodeStoreFailed, commitErr)
				}
				return existing, nil
			}
			return events.Event{}, events.ErrSequenceConflict
		}
		return events.Event{}, events.NewError(events.CodeStoreFailed, err)
	}
	seq, err := result.LastInsertId()
	if err != nil || seq <= 0 {
		return events.Event{}, events.NewError(events.CodeStoreFailed, err)
	}
	clean.Sequence = events.Sequence(seq)
	clean.At = at
	if err := tx.Commit(); err != nil {
		return events.Event{}, events.NewError(events.CodeStoreFailed, err)
	}
	return clean, nil
}

// Since returns durable events with sequence strictly greater than after.
func (s *Store) Since(ctx context.Context, after events.Sequence, limit int) ([]events.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, events.NewError(events.CodeStoreFailed, err)
	}
	if limit <= 0 || limit > 1000 {
		return nil, events.ErrInvalidEvent
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT sequence, event_id, event_type, subject,
			COALESCE(task_id, ''), COALESCE(run_id, ''), COALESCE(resource_id, ''), COALESCE(evidence_id, ''),
			timestamp, data_json, idempotency_key
		FROM structured_events
		WHERE sequence > ?
		ORDER BY sequence ASC
		LIMIT ?
	`, after, limit)
	if err != nil {
		return nil, events.NewError(events.CodeStoreFailed, err)
	}
	defer rows.Close()
	result := make([]events.Event, 0)
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	if err := rows.Err(); err != nil {
		return nil, events.NewError(events.CodeStoreFailed, err)
	}
	return result, nil
}

type eventScanner interface {
	Scan(...any) error
}

func scanEvent(scanner eventScanner) (events.Event, error) {
	var event events.Event
	var timestamp, dataJSON string
	if err := scanner.Scan(
		&event.Sequence, &event.ID, &event.Type, &event.Subject,
		&event.TaskID, &event.RunID, &event.ResourceID, &event.EvidenceID,
		&timestamp, &dataJSON, &event.IdempotencyKey,
	); err != nil {
		return events.Event{}, events.NewError(events.CodeStoreFailed, err)
	}
	at, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return events.Event{}, events.NewError(events.CodeStoreFailed, err)
	}
	event.At = at.UTC()
	if err := json.Unmarshal([]byte(dataJSON), &event.Data); err != nil {
		return events.Event{}, events.NewError(events.CodeStoreFailed, err)
	}
	if err := event.Validate(); err != nil {
		return events.Event{}, err
	}
	return events.CloneEvent(event), nil
}

func loadEventByIdempotency(ctx context.Context, tx *sql.Tx, key events.IdempotencyKey) (events.Event, string, bool, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT sequence, event_id, event_type, subject,
			COALESCE(task_id, ''), COALESCE(run_id, ''), COALESCE(resource_id, ''), COALESCE(evidence_id, ''),
			timestamp, data_json, idempotency_key, content_digest
		FROM structured_events WHERE idempotency_key = ?
	`, key)
	var event events.Event
	var timestamp, dataJSON, digest string
	err := row.Scan(
		&event.Sequence, &event.ID, &event.Type, &event.Subject,
		&event.TaskID, &event.RunID, &event.ResourceID, &event.EvidenceID,
		&timestamp, &dataJSON, &event.IdempotencyKey, &digest,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return events.Event{}, "", false, nil
	}
	if err != nil {
		return events.Event{}, "", false, err
	}
	at, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return events.Event{}, "", false, fmt.Errorf("parse structured event timestamp: %w", err)
	}
	event.At = at.UTC()
	if err := json.Unmarshal([]byte(dataJSON), &event.Data); err != nil {
		return events.Event{}, "", false, err
	}
	if err := event.Validate(); err != nil {
		return events.Event{}, "", false, err
	}
	return events.CloneEvent(event), digest, true, nil
}
