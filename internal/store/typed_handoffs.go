package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/protocol"
)

type typedHandoffRefs struct {
	Claims       map[string]string     `json:"claims"`
	EvidenceIDs  []protocol.EvidenceID `json:"evidence_ids"`
	ChangedFiles []string              `json:"changed_files"`
	Risks        []string              `json:"risks,omitempty"`
	Unresolved   []string              `json:"unresolved,omitempty"`
}

// Create persists a typed handoff in its own canonical table. It never reads
// or repurposes the legacy free-form handoffs compatibility history.
func (s *Store) Create(ctx context.Context, handoff protocol.Handoff) (protocol.Handoff, error) {
	if err := handoff.Validate(); err != nil {
		return protocol.Handoff{}, err
	}
	refs, err := json.Marshal(typedHandoffRefs{
		Claims: handoff.Claims, EvidenceIDs: handoff.EvidenceIDs, ChangedFiles: handoff.ChangedFiles,
		Risks: handoff.Risks, Unresolved: handoff.Unresolved,
	})
	if err != nil {
		return protocol.Handoff{}, protocol.ErrInvalid
	}
	for attempt := 0; ; attempt++ {
		stored, err := s.createTypedHandoffOnce(ctx, handoff, string(refs))
		if err == nil || !isSQLiteBusy(err) || attempt >= sqliteBusyRetries {
			return stored, err
		}
		if err := waitSQLiteRetry(ctx, attempt); err != nil {
			return protocol.Handoff{}, protocol.ErrUnavailable
		}
	}
}

func (s *Store) createTypedHandoffOnce(ctx context.Context, handoff protocol.Handoff, refs string) (protocol.Handoff, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return protocol.Handoff{}, protocol.ErrUnavailable
	}
	defer tx.Rollback()
	existing, err := typedHandoffByIdentity(ctx, tx, handoff.ID, handoff.IdempotencyKey)
	if err == nil {
		if sameTypedHandoff(existing, handoff) {
			_ = tx.Rollback()
			if err := s.appendTypedHandoffEvent(ctx, existing, protocol.StatusCreated); err != nil {
				return protocol.Handoff{}, err
			}
			return existing, nil
		}
		return protocol.Handoff{}, protocol.ErrTransitionInvalid
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return protocol.Handoff{}, protocol.ErrUnavailable
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO typed_handoffs(
			handoff_id, idempotency_key, version, task_id, sender_principal,
			target_role, status, refs_json, context_digest, created_at, consumed_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)
	`, handoff.ID, handoff.IdempotencyKey, handoff.Version, handoff.TaskID, handoff.FromAgent,
		handoff.ToRole, handoff.Status, refs, handoff.ContextDigest, handoff.CreatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return protocol.Handoff{}, protocol.ErrUnavailable
	}
	if err := tx.Commit(); err != nil {
		return protocol.Handoff{}, protocol.ErrUnavailable
	}
	if err := s.appendTypedHandoffEvent(ctx, handoff, protocol.StatusCreated); err != nil {
		return protocol.Handoff{}, err
	}
	return copyTypedHandoff(handoff), nil
}

func typedHandoffByIdentity(ctx context.Context, tx *sql.Tx, id protocol.HandoffID, key string) (protocol.Handoff, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT handoff_id, idempotency_key, version, task_id, sender_principal,
			target_role, status, refs_json, context_digest, created_at, consumed_at
		FROM typed_handoffs WHERE handoff_id = ? OR idempotency_key = ?
	`, id, key)
	return scanTypedHandoff(row)
}

func scanTypedHandoff(row interface{ Scan(...any) error }) (protocol.Handoff, error) {
	var handoff protocol.Handoff
	var refsJSON, created string
	var consumed sql.NullString
	if err := row.Scan(&handoff.ID, &handoff.IdempotencyKey, &handoff.Version, &handoff.TaskID, &handoff.FromAgent,
		&handoff.ToRole, &handoff.Status, &refsJSON, &handoff.ContextDigest, &created, &consumed); err != nil {
		return protocol.Handoff{}, err
	}
	var refs typedHandoffRefs
	if err := json.Unmarshal([]byte(refsJSON), &refs); err != nil {
		return protocol.Handoff{}, protocol.ErrInvalid
	}
	createdAt, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return protocol.Handoff{}, protocol.ErrInvalid
	}
	handoff.Claims, handoff.EvidenceIDs, handoff.ChangedFiles, handoff.Risks, handoff.Unresolved = refs.Claims, refs.EvidenceIDs, refs.ChangedFiles, refs.Risks, refs.Unresolved
	handoff.CreatedAt = createdAt
	if consumed.Valid {
		consumedAt, err := time.Parse(time.RFC3339Nano, consumed.String)
		if err != nil {
			return protocol.Handoff{}, protocol.ErrInvalid
		}
		handoff.ConsumedAt = &consumedAt
	}
	if err := handoff.Validate(); err != nil {
		return protocol.Handoff{}, protocol.ErrInvalid
	}
	return copyTypedHandoff(handoff), nil
}

func sameTypedHandoff(left, right protocol.Handoff) bool {
	// Lifecycle state and timestamps are assigned by authoritative boundaries,
	// so an uncertain caller retry must compare the immutable request content.
	left.Status, right.Status = protocol.StatusCreated, protocol.StatusCreated
	left.CreatedAt, right.CreatedAt = time.Time{}, time.Time{}
	left.ConsumedAt, right.ConsumedAt = nil, nil
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

func copyTypedHandoff(handoff protocol.Handoff) protocol.Handoff {
	copy := handoff
	copy.Claims = make(map[string]string, len(handoff.Claims))
	for key, value := range handoff.Claims {
		copy.Claims[key] = value
	}
	copy.EvidenceIDs = append([]protocol.EvidenceID(nil), handoff.EvidenceIDs...)
	copy.ChangedFiles = append([]string(nil), handoff.ChangedFiles...)
	copy.Risks = append([]string(nil), handoff.Risks...)
	copy.Unresolved = append([]string(nil), handoff.Unresolved...)
	if handoff.ConsumedAt != nil {
		at := *handoff.ConsumedAt
		copy.ConsumedAt = &at
	}
	return copy
}

func (s *Store) GetHandoff(ctx context.Context, id protocol.HandoffID) (protocol.Handoff, error) {
	if id == "" {
		return protocol.Handoff{}, protocol.ErrInvalid
	}
	handoff, err := scanTypedHandoff(s.db.QueryRowContext(ctx, `
		SELECT handoff_id, idempotency_key, version, task_id, sender_principal,
			target_role, status, refs_json, context_digest, created_at, consumed_at
		FROM typed_handoffs WHERE handoff_id = ?
	`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.Handoff{}, protocol.ErrHandoffNotFound
	}
	if err != nil {
		return protocol.Handoff{}, err
	}
	return handoff, nil
}

func (s *Store) Transition(ctx context.Context, id protocol.HandoffID, from, to protocol.Status, principal protocol.Principal) (protocol.Handoff, error) {
	if !legalHandoffTransition(from, to) || principal.ID == "" || principal.Role == "" {
		return protocol.Handoff{}, protocol.ErrTransitionInvalid
	}
	for attempt := 0; ; attempt++ {
		handoff, err := s.transitionTypedHandoffOnce(ctx, id, from, to, principal)
		if err == nil || !isSQLiteBusy(err) || attempt >= sqliteBusyRetries {
			return handoff, err
		}
		if err := waitSQLiteRetry(ctx, attempt); err != nil {
			return protocol.Handoff{}, protocol.ErrUnavailable
		}
	}
}

func (s *Store) transitionTypedHandoffOnce(ctx context.Context, id protocol.HandoffID, from, to protocol.Status, principal protocol.Principal) (protocol.Handoff, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return protocol.Handoff{}, protocol.ErrUnavailable
	}
	defer tx.Rollback()
	handoff, err := typedHandoffByIdentity(ctx, tx, id, "__no_idempotency_match__")
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.Handoff{}, protocol.ErrHandoffNotFound
	}
	if err != nil {
		return protocol.Handoff{}, protocol.ErrUnavailable
	}
	if handoff.Status != from || (to != protocol.StatusConsumed && principal.ID != handoff.FromAgent) ||
		(to == protocol.StatusConsumed && principal.Role != handoff.ToRole) {
		return protocol.Handoff{}, protocol.ErrTransitionInvalid
	}
	var consumed any
	if to == protocol.StatusConsumed {
		consumed = utcNow()
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE typed_handoffs SET status = ?, consumed_at = ? WHERE handoff_id = ? AND status = ?
	`, to, consumed, id, from)
	if err != nil {
		return protocol.Handoff{}, protocol.ErrUnavailable
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return protocol.Handoff{}, protocol.ErrTransitionInvalid
	}
	if err := tx.Commit(); err != nil {
		return protocol.Handoff{}, protocol.ErrUnavailable
	}
	updated, err := s.GetHandoff(ctx, id)
	if err != nil {
		return protocol.Handoff{}, err
	}
	if to == protocol.StatusAccepted || to == protocol.StatusRejected || to == protocol.StatusConsumed {
		if err := s.appendTypedHandoffEvent(ctx, updated, to); err != nil {
			return protocol.Handoff{}, err
		}
	}
	return updated, nil
}

func legalHandoffTransition(from, to protocol.Status) bool {
	return (from == protocol.StatusCreated && to == protocol.StatusValidated) ||
		(from == protocol.StatusValidated && (to == protocol.StatusAccepted || to == protocol.StatusRejected)) ||
		(from == protocol.StatusAccepted && to == protocol.StatusConsumed)
}

func (s *Store) appendTypedHandoffEvent(ctx context.Context, handoff protocol.Handoff, status protocol.Status) error {
	eventType := events.Type("handoff.created")
	suffix := "CREATED"
	if status == protocol.StatusAccepted {
		eventType, suffix = "handoff.accepted", "ACCEPTED"
	} else if status == protocol.StatusRejected {
		eventType, suffix = "handoff.rejected", "REJECTED"
	} else if status == protocol.StatusConsumed {
		eventType, suffix = "handoff.consumed", "CONSUMED"
	}
	_, err := s.Append(ctx, events.Event{
		ID:             events.EventID("EVENT-HANDOFF-" + suffix + "-" + string(handoff.ID)),
		Type:           eventType,
		Subject:        events.SubjectID(handoff.FromAgent),
		TaskID:         events.TaskID(handoff.TaskID),
		ResourceID:     events.ResourceID(handoff.ID),
		IdempotencyKey: events.IdempotencyKey("HANDOFF-" + suffix + "-" + string(handoff.ID)),
		Data: map[string]string{
			"context_digest": handoff.ContextDigest,
			"handoff_id":     string(handoff.ID),
			"result":         string(status),
			"target_role":    string(handoff.ToRole),
		},
	})
	if err != nil {
		return protocol.ErrUnavailable
	}
	return nil
}

// EvidenceBelongsToTask checks the T06-owned evidence metadata without
// copying evidence state into the typed handoff projection.
func (s *Store) EvidenceBelongsToTask(ctx context.Context, taskID protocol.TaskID, evidenceIDs []protocol.EvidenceID) error {
	for _, evidenceID := range evidenceIDs {
		var metadataJSON, state string
		err := s.db.QueryRowContext(ctx, "SELECT metadata_json, state FROM evidence_nodes WHERE node_id = ?", evidenceID).Scan(&metadataJSON, &state)
		if err != nil {
			return protocol.ErrEvidenceInvalid
		}
		var metadata map[string]string
		if json.Unmarshal([]byte(metadataJSON), &metadata) != nil || metadata["task_id"] != string(taskID) || state == "draft" {
			return protocol.ErrEvidenceInvalid
		}
	}
	return nil
}

var _ protocol.Repository = (*Store)(nil)
