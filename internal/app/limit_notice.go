package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

// LimitNoticeCategory is the outbox category of byte-cap refusal notices
// delivered to the Boards (Activity) and the Marshal chat.
const LimitNoticeCategory = "limit"

// LimitNotice is the redacted, durable announcement of an adapter refusal
// caused by a byte cap. It is delivered at least once to each consumer and
// deduplicated by EventID.
type LimitNotice struct {
	EventID string      `json:"eventId"`
	RunID   string      `json:"runId,omitempty"`
	Source  string      `json:"source,omitempty"`
	Summary string      `json:"summary"`
	Next    string      `json:"next,omitempty"`
	Limit   model.Limit `json:"limit"`
	At      time.Time   `json:"at"`
}

// FormatLimitNoticeSummary renders a redacted, single-line summary of a byte
// cap refusal naming what reached the limit and both sizes.
func FormatLimitNoticeSummary(limit model.Limit) string {
	subject := RedactApprovalText(limit.Subject)
	if subject == "" {
		subject = RedactApprovalText(limit.Kind)
	}
	if subject == "" {
		subject = "request"
	}
	return boundedRedacted(
		fmt.Sprintf("Limit reached: %s needs %d bytes, over the %d-byte limit.", subject, limit.ActualBytes, limit.CapBytes),
		500,
	)
}

// FormatLimitActivity formats a limit notice for Board Activity lines.
func FormatLimitActivity(summary, next string) string {
	summary = boundedRedacted(summary, 500)
	next = boundedRedacted(next, 500)
	if next == "" {
		return summary
	}
	if summary == "" {
		return next
	}
	return summary + " " + next
}

// RecordLimitNotice records a redacted byte-cap refusal in the durable notice
// outbox and audit event log in one transaction so both the Boards (Activity)
// and the Marshal chat receive the same cause and next step. Repeated calls
// for the same refusal share one stable EventID and record once.
func (r *Runtime) RecordLimitNotice(ctx context.Context, runID, source string, limit model.Limit) error {
	if r == nil || r.store == nil {
		return nil
	}
	redacted := model.Limit{
		Kind:        RedactApprovalText(limit.Kind),
		Subject:     boundedRedacted(limit.Subject, 256),
		ActualBytes: limit.ActualBytes,
		CapBytes:    limit.CapBytes,
		Limited:     true,
		KeptBytes:   limit.KeptBytes,
		Cursor:      boundedRedacted(limit.Cursor, 256),
		EvidenceRef: boundedRedacted(limit.EvidenceRef, 256),
		Action:      boundedRedacted(limit.Action, 500),
	}
	source = boundedRedacted(source, 128)
	summary := FormatLimitNoticeSummary(redacted)
	next := redacted.Action
	sum := sha256.Sum256([]byte(strings.Join([]string{
		runID, source, redacted.Kind, redacted.Subject,
		fmt.Sprintf("%d:%d", redacted.ActualBytes, redacted.CapBytes),
		redacted.Cursor, redacted.EvidenceRef, next,
	}, "\x00")))
	eventID := "limit:" + hex.EncodeToString(sum[:8])
	now := time.Now().UTC()
	notice := LimitNotice{
		EventID: eventID,
		RunID:   runID,
		Source:  source,
		Summary: summary,
		Next:    next,
		Limit:   redacted,
		At:      now,
	}
	payload, err := json.Marshal(notice)
	if err != nil {
		return err
	}
	project, err := r.store.Project(context.WithoutCancel(ctx))
	if err != nil {
		return err
	}
	data := map[string]any{
		"run_id":       runID,
		"source":       source,
		"kind":         redacted.Kind,
		"subject":      redacted.Subject,
		"actual_bytes": redacted.ActualBytes,
		"cap_bytes":    redacted.CapBytes,
		"summary":      summary,
		"next":         next,
		"limit":        redacted,
	}
	entry := store.NoticeOutboxEntry{
		EventID:     eventID,
		ProjectID:   r.ProjectIdentity(),
		RunID:       runID,
		Category:    LimitNoticeCategory,
		Transition:  "refused",
		PhaseOrder:  1,
		Status:      redacted.Kind,
		Command:     next,
		Summary:     summary,
		PayloadJSON: string(payload),
		CreatedAt:   now,
	}
	auditEvent := model.Event{
		ID:        eventID,
		Type:      "marshal.limit_notice",
		ProjectID: project.ID,
		Timestamp: now,
		Data:      data,
	}
	_, err = r.store.RecordNoticeWithEvent(context.WithoutCancel(ctx), entry, auditEvent)
	return err
}

// RecordLimitError records a durable limit notice when err carries a typed
// model.Limit; other errors are ignored.
func (r *Runtime) RecordLimitError(ctx context.Context, runID, source string, err error) error {
	limit, ok := model.LimitOf(err)
	if !ok {
		return nil
	}
	return r.RecordLimitNotice(ctx, runID, source, limit)
}

// RunNoticeLimited is the state of a notice for a live submission that was
// refused at a byte cap before it was admitted.
const RunNoticeLimited = "limited"

// RecordSubmissionLimitNotice durably records the notice of a live
// submission refused at a byte cap before it was admitted. Unlike
// RecordLimitNotice, which announces a refused read or adapter step, it is a
// run notice whose event ID the caller derives from the submission: one
// outbox entry and its audit event, which the Boards' Activity and the
// Marshal chat both read. A repeated event ID records nothing and reports
// false. It admits no submission and changes nothing else in the project.
func (r *Runtime) RecordSubmissionLimitNotice(ctx context.Context, n RunNotice) (bool, error) {
	if r == nil || r.store == nil {
		return false, model.ErrUnavailable
	}
	if n.At.IsZero() {
		n.At = time.Now().UTC()
	}
	n.State = RunNoticeLimited
	n.Summary = boundedRedacted(n.Summary, 500)
	n.Next = boundedRedacted(n.Next, 500)
	payload, err := json.Marshal(n)
	if err != nil {
		return false, err
	}
	data := map[string]any{"run_id": n.RunID, "state": n.State, "code": n.Code, "summary": n.Summary, "next": n.Next}
	if n.Limit != nil {
		data["limit"] = *n.Limit
	}
	entry := store.NoticeOutboxEntry{EventID: n.EventID, ProjectID: r.ProjectIdentity(), RunID: n.RunID, Category: RunNoticeCategory,
		Transition: n.State, PhaseOrder: 1, Status: n.Code, Command: n.Next, Summary: n.Summary, PayloadJSON: string(payload), CreatedAt: n.At}
	return r.store.RecordNoticeWithEvent(context.WithoutCancel(ctx), entry, model.Event{ID: n.EventID, Type: "marshal.run_notice", ProjectID: r.ProjectID(), Timestamp: n.At, Data: data})
}
