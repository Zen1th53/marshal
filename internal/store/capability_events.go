package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/capability"
)

func (s *Store) AppendCapabilityEvent(ctx context.Context, event capability.AuditEvent) error {
	if event.ID == "" || event.Type == "" || event.Timestamp.IsZero() || !event.Reason.Valid() || !event.Kind.Valid() || strings.TrimSpace(event.Resource) == "" || len(event.Resource) > 1024 {
		return fmt.Errorf("invalid capability audit event")
	}
	data, err := json.Marshal(map[string]string{
		"grant_id": event.GrantID,
		"subject":  event.Subject,
		"kind":     string(event.Kind),
		"resource": event.Resource,
		"reason":   string(event.Reason),
	})
	if err != nil {
		return fmt.Errorf("encode capability audit event: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin capability audit event: %w", err)
	}
	defer tx.Rollback()
	var existing string
	err = tx.QueryRowContext(ctx, "SELECT data_json FROM audit_events WHERE event_id = ?", event.ID).Scan(&existing)
	if err == nil {
		if existing != string(data) {
			return fmt.Errorf("capability audit event ID conflict")
		}
		return nil
	}
	if err != sql.ErrNoRows {
		return fmt.Errorf("read capability audit event: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO audit_events(event_id, event_type, task_id, aggregate_revision, timestamp, data_json)
		VALUES(?, ?, NULLIF(?, ''), 0, ?, ?)
	`, event.ID, event.Type, event.TaskID, event.Timestamp.UTC().Format(time.RFC3339Nano), string(data)); err != nil {
		return fmt.Errorf("insert capability audit event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit capability audit event: %w", err)
	}
	return nil
}
