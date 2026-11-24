package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

const noticeOutboxSchema = `
CREATE TABLE IF NOT EXISTS marshal_notice_outbox (
	seq INTEGER PRIMARY KEY AUTOINCREMENT,
	event_id TEXT NOT NULL UNIQUE,
	project_id TEXT NOT NULL,
	run_id TEXT NOT NULL,
	task_id TEXT NOT NULL DEFAULT '',
	approval_id TEXT NOT NULL DEFAULT '',
	category TEXT NOT NULL,
	transition TEXT NOT NULL,
	phase_order INTEGER NOT NULL DEFAULT 0,
	status TEXT NOT NULL,
	authority TEXT NOT NULL DEFAULT '',
	destination TEXT NOT NULL DEFAULT '',
	command TEXT NOT NULL DEFAULT '',
	replacement_id TEXT NOT NULL DEFAULT '',
	summary TEXT NOT NULL DEFAULT '',
	payload_json TEXT NOT NULL DEFAULT '{}',
	created_at TEXT NOT NULL,
	acked_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_marshal_notice_outbox_pending
	ON marshal_notice_outbox(project_id, acked_at, phase_order, seq);
CREATE INDEX IF NOT EXISTS idx_marshal_notice_outbox_run
	ON marshal_notice_outbox(project_id, run_id, seq);
CREATE TABLE IF NOT EXISTS marshal_notice_inbox (
	consumer TEXT NOT NULL,
	event_id TEXT NOT NULL,
	admitted_at TEXT NOT NULL,
	rendered_text TEXT NOT NULL DEFAULT '',
	PRIMARY KEY(consumer, event_id)
);
`

func init() {
	registerCanonicalMigration(114, ddlMigration(noticeOutboxSchema))
}

// NoticeOutboxEntry is a durable, ordered notification record written in the
// same transaction as its durable audit event.
type NoticeOutboxEntry struct {
	Seq           int64     `json:"seq"`
	EventID       string    `json:"event_id"`
	ProjectID     string    `json:"project_id"`
	RunID         string    `json:"run_id"`
	TaskID        string    `json:"task_id,omitempty"`
	ApprovalID    string    `json:"approval_id,omitempty"`
	Category      string    `json:"category"`
	Transition    string    `json:"transition"`
	PhaseOrder    int       `json:"phase_order"`
	Status        string    `json:"status"`
	Authority     string    `json:"authority,omitempty"`
	Destination   string    `json:"destination,omitempty"`
	Command       string    `json:"command,omitempty"`
	ReplacementID string    `json:"replacement_id,omitempty"`
	Summary       string    `json:"summary,omitempty"`
	PayloadJSON   string    `json:"payload_json,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	AckedAt       string    `json:"acked_at,omitempty"`
}

// NoticeInboxEntry records durable admission of a notice event by a consumer.
type NoticeInboxEntry struct {
	Consumer     string    `json:"consumer"`
	EventID      string    `json:"event_id"`
	AdmittedAt   time.Time `json:"admitted_at"`
	RenderedText string    `json:"rendered_text,omitempty"`
}

// RecordNoticeWithEvent atomically persists an outbox entry and its matching
// durable audit event in one SQLite transaction. Repeated calls with the same
// stable EventID are idempotent and return created=false.
func (s *Store) RecordNoticeWithEvent(ctx context.Context, entry NoticeOutboxEntry, auditEvent model.Event) (bool, error) {
	if strings.TrimSpace(entry.EventID) == "" || strings.TrimSpace(entry.ProjectID) == "" || strings.TrimSpace(entry.Category) == "" || strings.TrimSpace(entry.Transition) == "" {
		return false, fmt.Errorf("%w: incomplete notice outbox entry", model.ErrInvalid)
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	if strings.TrimSpace(entry.PayloadJSON) == "" {
		entry.PayloadJSON = "{}"
	}
	if auditEvent.ID == "" {
		auditEvent.ID = entry.EventID
	}
	if auditEvent.ProjectID == "" {
		auditEvent.ProjectID = entry.ProjectID
	}
	if auditEvent.Timestamp.IsZero() {
		auditEvent.Timestamp = entry.CreatedAt
	}

	var created bool
	for attempt := 0; ; attempt++ {
		var err error
		created, err = s.recordNoticeWithEventOnce(ctx, entry, auditEvent)
		if !isSQLiteBusy(err) || attempt >= sqliteBusyRetries {
			return created, err
		}
		if err := waitSQLiteRetry(ctx, attempt); err != nil {
			return false, err
		}
	}
}

func (s *Store) recordNoticeWithEventOnce(ctx context.Context, entry NoticeOutboxEntry, auditEvent model.Event) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	created, err := s.recordNoticeWithEventTx(ctx, tx, entry, auditEvent)
	if err != nil {
		return false, err
	}
	return created, tx.Commit()
}

// NoticeWrite pairs a durable notice with its audit event.
type NoticeWrite struct {
	Entry NoticeOutboxEntry
	Event model.Event
}

func (s *Store) recordNoticeWithEventTx(ctx context.Context, tx *sql.Tx, entry NoticeOutboxEntry, auditEvent model.Event) (bool, error) {
	res, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO marshal_notice_outbox (
			event_id, project_id, run_id, task_id, approval_id, category, transition,
			phase_order, status, authority, destination, command, replacement_id,
			summary, payload_json, created_at, acked_at
		) SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ''
		WHERE NOT EXISTS (SELECT 1 FROM audit_events WHERE event_id = ?)`,
		entry.EventID, entry.ProjectID, entry.RunID, entry.TaskID, entry.ApprovalID,
		entry.Category, entry.Transition, entry.PhaseOrder, entry.Status, entry.Authority,
		entry.Destination, entry.Command, entry.ReplacementID, entry.Summary,
		entry.PayloadJSON, entry.CreatedAt.UTC().Format(time.RFC3339Nano), entry.EventID,
	)
	if err != nil {
		return false, fmt.Errorf("insert notice outbox: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected > 0 {
		if err := s.appendEventTx(ctx, tx, auditEvent); err != nil {
			return false, fmt.Errorf("append notice audit event: %w", err)
		}
	}
	return affected > 0, nil
}

// PendingNotices returns unacknowledged outbox entries ordered with opened
// entries ahead of terminal/closed entries (phase_order ASC, seq ASC).
func (s *Store) PendingNotices(ctx context.Context, projectID, runID string, limit int) ([]NoticeOutboxEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	var rows *sql.Rows
	var err error
	if runID != "" {
		rows, err = s.db.QueryContext(ctx, `
			SELECT seq, event_id, project_id, run_id, task_id, approval_id, category, transition,
			       phase_order, status, authority, destination, command, replacement_id,
			       summary, payload_json, created_at, acked_at
			FROM marshal_notice_outbox
			WHERE project_id = ? AND run_id = ? AND acked_at = ''
			ORDER BY phase_order ASC, seq ASC
			LIMIT ?`, projectID, runID, limit)
	} else {
		rows, err = s.db.QueryContext(ctx, `
			SELECT seq, event_id, project_id, run_id, task_id, approval_id, category, transition,
			       phase_order, status, authority, destination, command, replacement_id,
			       summary, payload_json, created_at, acked_at
			FROM marshal_notice_outbox
			WHERE project_id = ? AND acked_at = ''
			ORDER BY phase_order ASC, seq ASC
			LIMIT ?`, projectID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNoticeOutboxRows(rows)
}

// ListNotices returns durable outbox entries for a project (optionally scoped
// to one run), newest first for Inbox, Board and MCP projections.
func (s *Store) ListNotices(ctx context.Context, projectID, runID string, limit int) ([]NoticeOutboxEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	var rows *sql.Rows
	var err error
	if runID != "" {
		rows, err = s.db.QueryContext(ctx, `
			SELECT seq, event_id, project_id, run_id, task_id, approval_id, category, transition,
			       phase_order, status, authority, destination, command, replacement_id,
			       summary, payload_json, created_at, acked_at
			FROM marshal_notice_outbox
			WHERE project_id = ? AND run_id = ?
			ORDER BY seq DESC
			LIMIT ?`, projectID, runID, limit)
	} else {
		rows, err = s.db.QueryContext(ctx, `
			SELECT seq, event_id, project_id, run_id, task_id, approval_id, category, transition,
			       phase_order, status, authority, destination, command, replacement_id,
			       summary, payload_json, created_at, acked_at
			FROM marshal_notice_outbox
			WHERE project_id = ?
			ORDER BY seq DESC
			LIMIT ?`, projectID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNoticeOutboxRows(rows)
}

func scanNoticeOutboxRows(rows *sql.Rows) ([]NoticeOutboxEntry, error) {
	var out []NoticeOutboxEntry
	for rows.Next() {
		var e NoticeOutboxEntry
		var createdAt string
		if err := rows.Scan(
			&e.Seq, &e.EventID, &e.ProjectID, &e.RunID, &e.TaskID, &e.ApprovalID,
			&e.Category, &e.Transition, &e.PhaseOrder, &e.Status, &e.Authority,
			&e.Destination, &e.Command, &e.ReplacementID, &e.Summary, &e.PayloadJSON,
			&createdAt, &e.AckedAt,
		); err != nil {
			return nil, err
		}
		e.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		out = append(out, e)
	}
	return out, rows.Err()
}

// AdmitNotice records a notice event in the consumer's durable accepting inbox
// by stable event ID. It returns admitted=true the first time the event is
// admitted for this consumer, and admitted=false on replay/dedup.
func (s *Store) AdmitNotice(ctx context.Context, consumer, eventID, renderedText string, now time.Time) (bool, error) {
	if strings.TrimSpace(consumer) == "" || strings.TrimSpace(eventID) == "" {
		return false, fmt.Errorf("%w: consumer and event_id are required", model.ErrInvalid)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO marshal_notice_inbox(consumer, event_id, admitted_at, rendered_text)
		VALUES (?, ?, ?, ?)`,
		consumer, eventID, now.UTC().Format(time.RFC3339Nano), "",
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// NoticeAdmitted reports whether consumer already admitted eventID.
func (s *Store) NoticeAdmitted(ctx context.Context, consumer, eventID string) (bool, error) {
	var admittedAt string
	err := s.db.QueryRowContext(ctx, `SELECT admitted_at FROM marshal_notice_inbox WHERE consumer = ? AND event_id = ?`, consumer, eventID).Scan(&admittedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// ListAdmittedNotices returns entries admitted into the consumer's accepting inbox.
func (s *Store) ListAdmittedNotices(ctx context.Context, consumer string) ([]NoticeInboxEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT consumer, event_id, admitted_at, rendered_text
		FROM marshal_notice_inbox
		WHERE consumer = ?
		ORDER BY rowid ASC`, consumer)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NoticeInboxEntry
	for rows.Next() {
		var e NoticeInboxEntry
		var at string
		if err := rows.Scan(&e.Consumer, &e.EventID, &at, &e.RenderedText); err != nil {
			return nil, err
		}
		e.AdmittedAt, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, e)
	}
	return out, rows.Err()
}

// AckNotice marks an outbox entry acknowledged only after durable admission.
func (s *Store) AckNotice(ctx context.Context, eventID string, now time.Time) error {
	if strings.TrimSpace(eventID) == "" {
		return fmt.Errorf("%w: event_id is required", model.ErrInvalid)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE marshal_notice_outbox
		SET acked_at = ?
		WHERE event_id = ? AND acked_at = ''`,
		now.UTC().Format(time.RFC3339Nano), eventID,
	)
	return err
}

// ApprovalNoticeRun returns the exact persisted run binding of an approval.
func (s *Store) ApprovalNoticeRun(ctx context.Context, projectID, approvalID string) (string, error) {
	var runID string
	err := s.db.QueryRowContext(ctx, `SELECT run_id FROM marshal_notice_outbox WHERE project_id = ? AND approval_id = ? ORDER BY seq LIMIT 1`, projectID, approvalID).Scan(&runID)
	if errors.Is(err, sql.ErrNoRows) {
		var data string
		err = s.db.QueryRowContext(ctx, `SELECT data_json FROM audit_events WHERE event_id IN (?, ?, ?) ORDER BY event_id LIMIT 1`, "approval:"+approvalID+":opened", "approval:"+approvalID+":terminal", "approval:"+approvalID+":revoked").Scan(&data)
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		var binding struct {
			RunID string `json:"marshal_run_id"`
		}
		if err := json.Unmarshal([]byte(data), &binding); err != nil {
			return "", err
		}
		return binding.RunID, nil
	}
	return runID, err
}

// ReleaseNotice permits retry after a consumer rejects delivery.
func (s *Store) ReleaseNotice(ctx context.Context, consumer, eventID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM marshal_notice_inbox WHERE consumer = ? AND event_id = ?`, consumer, eventID)
	return err
}

// PruneNotices removes acknowledged delivery records after the retention window.
// Audit events retain stable event IDs so reconciliation cannot recreate them.
func (s *Store) PruneNotices(ctx context.Context, before time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	cutoff := before.UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `DELETE FROM marshal_notice_inbox WHERE event_id IN (SELECT event_id FROM marshal_notice_outbox WHERE acked_at != '' AND acked_at < ?)`, cutoff); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM marshal_notice_outbox WHERE acked_at != '' AND acked_at < ?`, cutoff); err != nil {
		return err
	}
	return tx.Commit()
}
