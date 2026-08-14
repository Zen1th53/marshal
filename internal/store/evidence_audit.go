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
		reflected := reflect.ValueOf(value)
		if reflected.IsValid() && reflected.Kind() == reflect.String && reflected.Len() > maxEvidenceAuditValue {
			return fmt.Errorf("%w: evidence audit field %s is too large", model.ErrInvalid, key)
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
