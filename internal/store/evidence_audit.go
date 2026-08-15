package store

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

const maxEvidenceAuditValue = 256

// appendEvidenceEvent records the durable, structured audit fact for an
// evidence mutation in the caller's transaction. It is intentionally a
// narrow bridge to the existing audit_events table; global sequencing and
// delivery remain owned by the future event-stream subsystem.
func (s *Store) appendEvidenceEvent(ctx context.Context, tx *sql.Tx, eventType, taskID, actorID string, data map[string]any) error {
	for key, value := range map[string]string{"event_type": eventType, "task_id": taskID, "actor_id": actorID} {
		if len(value) > maxEvidenceAuditValue {
			return fmt.Errorf("%w: evidence audit %s is too large", model.ErrInvalid, key)
		}
	}
	for key, value := range data {
		if err := validateEvidenceAuditValue(key, value); err != nil {
			return err
		}
	}
	eventID, err := model.NewID("EVENT-")
	if err != nil {
		return err
	}
	return s.AppendEvent(ctx, tx, model.Event{
		ID: eventID, Type: eventType, TaskID: taskID, ActorAgentID: actorID,
		Timestamp: time.Now().UTC(), AggregateRevision: 0, Data: data,
	})
}

// validateEvidenceAuditValue deliberately permits only scalar correlation
// values. Nested maps, slices, and arbitrary structs could carry unbounded or
// secret-bearing provider/error payloads into audit_events, so they are
// rejected before JSON encoding and before the caller's transaction commits.
func validateEvidenceAuditValue(key string, value any) error {
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() {
		return fmt.Errorf("%w: evidence audit field %s is nil", model.ErrInvalid, key)
	}
	switch reflected.Kind() {
	case reflect.String:
		if reflected.Len() > maxEvidenceAuditValue {
			return fmt.Errorf("%w: evidence audit field %s is too large", model.ErrInvalid, key)
		}
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		// Scalar values are bounded by their fixed representation.
	default:
		return fmt.Errorf("%w: evidence audit field %s must be scalar", model.ErrInvalid, key)
	}
	return nil
}

func (s *Store) recordEvidenceEvent(ctx context.Context, eventType string, data map[string]any) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.appendEvidenceEvent(ctx, tx, eventType, "", "", data); err != nil {
		return err
	}
	return tx.Commit()
}
