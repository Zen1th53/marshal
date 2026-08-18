package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/netpolicy"
)

// PutEgressDecision stores only the bounded normalized request/decision
// projection. Policy rules and DNS resolution remain outside this projection.
// Repeating the exact ID and idempotency key is safe; conflicting reuse is a
// durable conflict.
func (s *Store) PutEgressDecision(ctx context.Context, record netpolicy.DecisionRecord) error {
	for attempt := 0; ; attempt++ {
		err := s.putEgressDecisionOnce(ctx, record)
		if err == nil || !isSQLiteBusy(err) || attempt >= sqliteBusyRetries {
			return err
		}
		if waitErr := waitSQLiteRetry(ctx, attempt); waitErr != nil {
			return fmt.Errorf("%w: retry egress decision persistence: %v", model.ErrUnavailable, waitErr)
		}
	}
}

func (s *Store) putEgressDecisionOnce(ctx context.Context, record netpolicy.DecisionRecord) error {
	if err := record.Validate(); err != nil {
		return fmt.Errorf("%w: invalid egress decision", model.ErrInvalid)
	}
	requestJSON, err := json.Marshal(record.Request)
	if err != nil {
		return fmt.Errorf("%w: encode egress request", model.ErrInvalid)
	}
	decisionJSON, err := json.Marshal(record.Decision)
	if err != nil {
		return fmt.Errorf("%w: encode egress decision", model.ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("%w: begin egress decision persistence", model.ErrUnavailable)
	}
	defer tx.Rollback()
	var storedKey, storedRequest, storedDecision, storedCreated string
	err = tx.QueryRowContext(ctx, `
		SELECT idempotency_key, request_json, decision_json, created_at
		FROM egress_decisions WHERE decision_id = ?
	`, record.ID).Scan(&storedKey, &storedRequest, &storedDecision, &storedCreated)
	if err == nil {
		if storedKey == record.IdempotencyKey && storedRequest == string(requestJSON) && storedDecision == string(decisionJSON) && storedCreated == record.CreatedAt.Format(time.RFC3339Nano) {
			_ = tx.Rollback()
			if record.Request.SubjectID != "" {
				if eventErr := s.appendEgressEvents(ctx, record); eventErr != nil {
					return eventErr
				}
			}
			return nil
		}
		return fmt.Errorf("%w: egress decision identity is immutable", model.ErrConflict)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: read egress decision", model.ErrUnavailable)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO egress_decisions(decision_id, idempotency_key, request_json, decision_json, created_at)
		VALUES(?, ?, ?, ?, ?)
	`, record.ID, record.IdempotencyKey, string(requestJSON), string(decisionJSON), record.CreatedAt.Format(time.RFC3339Nano)); err != nil {
		_ = tx.Rollback()
		if existing, readErr := s.GetEgressDecision(ctx, record.ID); readErr == nil && existing == record {
			if record.Request.SubjectID != "" {
				if eventErr := s.appendEgressEvents(ctx, record); eventErr != nil {
					return eventErr
				}
			}
			return nil
		}
		if isSQLiteBusy(err) {
			return fmt.Errorf("%w: insert egress decision: %w", model.ErrUnavailable, err)
		}
		return fmt.Errorf("%w: insert egress decision", model.ErrConflict)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%w: commit egress decision", model.ErrUnavailable)
	}
	if record.Request.SubjectID != "" {
		if err := s.appendEgressEvents(ctx, record); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) appendEgressEvents(ctx context.Context, record netpolicy.DecisionRecord) error {
	at := record.CreatedAt.UTC()
	if at.IsZero() {
		at = time.Now().UTC()
	}
	base := events.Event{
		Subject:    record.Request.SubjectID,
		TaskID:     record.Request.TaskID,
		ResourceID: "egress-" + record.ID,
		At:         at,
		Data:       map[string]any{"change_id": record.Request.ChangeID, "decision_id": record.ID, "protocol": string(record.Request.Protocol), "port": strconv.Itoa(record.Request.Port)},
	}
	requested := base
	requested.ID = "EVENT-NETWORK-EGRESS-REQUESTED-" + record.ID
	requested.Type = events.EventTypeNetworkEgressRequested
	requested.IdempotencyKey = "NETWORK-EGRESS-REQUESTED-" + record.ID
	requested.Data["result"] = "requested"
	if _, err := s.Append(ctx, requested); err != nil {
		return fmt.Errorf("append network egress requested event: %w", err)
	}
	result := base
	result.ID = "EVENT-NETWORK-EGRESS-RESULT-" + record.ID
	result.IdempotencyKey = "NETWORK-EGRESS-RESULT-" + record.ID
	result.Data["reason"] = string(record.Decision.Reason)
	result.Data["result"] = "denied"
	result.Type = events.EventTypeNetworkEgressDenied
	if record.Decision.Allowed {
		result.Type = events.EventTypeNetworkEgressAllowed
		result.Data["result"] = "allowed"
		result.Data["rule_id"] = string(record.Decision.RuleID)
	}
	if _, err := s.Append(ctx, result); err != nil {
		return fmt.Errorf("append network egress result event: %w", err)
	}
	return nil
}

func (s *Store) GetEgressDecision(ctx context.Context, id string) (netpolicy.DecisionRecord, error) {
	if id == "" {
		return netpolicy.DecisionRecord{}, fmt.Errorf("%w: decision id is required", model.ErrInvalid)
	}
	var record netpolicy.DecisionRecord
	var requestJSON, decisionJSON, created string
	err := s.db.QueryRowContext(ctx, `
		SELECT idempotency_key, request_json, decision_json, created_at
		FROM egress_decisions WHERE decision_id = ?
	`, id).Scan(&record.IdempotencyKey, &requestJSON, &decisionJSON, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return netpolicy.DecisionRecord{}, model.ErrNotFound
	}
	if err != nil {
		return netpolicy.DecisionRecord{}, fmt.Errorf("%w: read egress decision", model.ErrUnavailable)
	}
	record.ID = id
	if err := json.Unmarshal([]byte(requestJSON), &record.Request); err != nil {
		return netpolicy.DecisionRecord{}, fmt.Errorf("%w: invalid durable egress request", model.ErrInvalid)
	}
	if err := json.Unmarshal([]byte(decisionJSON), &record.Decision); err != nil {
		return netpolicy.DecisionRecord{}, fmt.Errorf("%w: invalid durable egress decision", model.ErrInvalid)
	}
	parsed, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return netpolicy.DecisionRecord{}, fmt.Errorf("%w: invalid durable egress timestamp", model.ErrInvalid)
	}
	record.CreatedAt = parsed
	if err := record.Validate(); err != nil {
		return netpolicy.DecisionRecord{}, fmt.Errorf("%w: corrupt durable egress decision", model.ErrInvalid)
	}
	return record, nil
}
