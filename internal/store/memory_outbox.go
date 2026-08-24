package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// MemoryOutboxEvent represents a durable event published for derived index consumers.
type MemoryOutboxEvent struct {
	EventID     string     `json:"event_id"`
	ProjectID   string     `json:"project_id"`
	MemoryID    string     `json:"memory_id"`
	EventType   string     `json:"event_type"`
	PayloadJSON string     `json:"payload_json"`
	CreatedAt   time.Time  `json:"created_at"`
	ProcessedAt *time.Time `json:"processed_at,omitempty"`
}

// MemoryOutboxPointer is intentionally small. Outbox events are notifications,
// never a second source of truth; consumers reload the canonical row by ID.
type MemoryOutboxPointer struct {
	MemoryID      string                `json:"memory_id"`
	Revision      int64                 `json:"revision"`
	Lifecycle     model.MemoryLifecycle `json:"lifecycle"`
	ContentDigest string                `json:"content_digest,omitempty"`
}

// insertMemoryOutboxTx records an outbox event within an existing transaction.
func insertMemoryOutboxTx(ctx context.Context, tx *sql.Tx, projectID, memoryID, eventType string, payload any) error {
	var idBytes [16]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return fmt.Errorf("generate outbox event id: %w", err)
	}
	eventID := "OUTBOX-" + hex.EncodeToString(idBytes[:])

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal outbox payload: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO memory_outbox (
			event_id, project_id, memory_id, event_type, payload_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?)
	`, eventID, projectID, memoryID, eventType, string(payloadBytes), now)
	if err != nil {
		return fmt.Errorf("insert memory outbox: %w", err)
	}
	return nil
}

// FetchUnprocessedMemoryOutbox returns pending unacknowledged outbox events for a project.
func (s *Store) FetchUnprocessedMemoryOutbox(ctx context.Context, projectID string, limit int) ([]MemoryOutboxEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT event_id, project_id, memory_id, event_type, payload_json, created_at, processed_at
		FROM memory_outbox
		WHERE project_id = ? AND processed_at IS NULL
		ORDER BY created_at ASC
		LIMIT ?
	`, projectID, limit)
	if err != nil {
		return nil, fmt.Errorf("fetch memory outbox: %w", err)
	}
	defer rows.Close()

	var events []MemoryOutboxEvent
	for rows.Next() {
		var (
			e           MemoryOutboxEvent
			createdAt   string
			processedAt sql.NullString
		)
		if err := rows.Scan(
			&e.EventID, &e.ProjectID, &e.MemoryID, &e.EventType,
			&e.PayloadJSON, &createdAt, &processedAt,
		); err != nil {
			return nil, fmt.Errorf("scan memory outbox: %w", err)
		}
		t, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse outbox created_at: %w", err)
		}
		e.CreatedAt = t
		if processedAt.Valid {
			pt, err := time.Parse(time.RFC3339Nano, processedAt.String)
			if err == nil {
				e.ProcessedAt = &pt
			}
		}
		events = append(events, e)
	}
	return events, nil
}

// AckMemoryOutbox marks outbox events as processed.
func (s *Store) AckMemoryOutbox(ctx context.Context, eventIDs []string) error {
	if len(eventIDs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin ack outbox: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	stmt, err := tx.PrepareContext(ctx, `UPDATE memory_outbox SET processed_at = ? WHERE event_id = ?`)
	if err != nil {
		return fmt.Errorf("prepare ack outbox: %w", err)
	}
	defer stmt.Close()

	for _, id := range eventIDs {
		if _, err := stmt.ExecContext(ctx, now, id); err != nil {
			return fmt.Errorf("ack outbox event %s: %w", id, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit ack outbox: %w", err)
	}
	return nil
}
