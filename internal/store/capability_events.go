package store

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/model"
)

// AppendCapabilityEvent persists the broker's secret-free audit projection.
// Foreign task identities remain in event data until the owning task service
// can validate them; no capability event invents a task row.
func (s *Store) AppendCapabilityEvent(ctx context.Context, event capability.AuditEvent) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin capability audit: %w", err)
	}
	defer tx.Rollback()
	if err := s.AppendEvent(ctx, tx, model.Event{
		ID: event.ID, Type: event.Type, Timestamp: event.Timestamp,
		AggregateRevision: 0,
		Data: map[string]any{
			"grant_id": event.GrantID, "subject": event.Subject, "task_id": event.TaskID,
			"kind": event.Kind, "resource": event.Resource, "reason": event.Reason,
		},
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit capability audit: %w", err)
	}
	return nil
}

var _ capability.AuditSink = (*Store)(nil)
