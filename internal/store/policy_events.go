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

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policy"
)

const maxPolicyEventField = 256

func policyEventType(request policy.PolicyMutationRequest) policy.PolicyEventType {
	if request.TargetState == policy.StateActive {
		return policy.EventPolicyActivated
	}
	return policy.EventPolicyDecisionAllowed
}

func policyEventData(request policy.PolicyMutationRequest, result, reason string) map[string]any {
	return map[string]any{
		"policy_id":      string(request.PolicyID),
		"policy_version": int64(request.PolicyVersion),
		"policy_digest":  string(request.Binding.Digest),
		"generation":     request.Binding.Generation,
		"previous_state": string(request.ExpectedState),
		"target_state":   string(request.TargetState),
		"action":         string(request.Action),
		"subject_id":     request.SubjectID,
		"session_id":     request.SessionID,
		"task_id":        request.TaskID,
		"change_id":      request.ChangeID,
		"result":         result,
		"reason_code":    reason,
	}
}

func validatePolicyEventData(data map[string]any) error {
	for key, value := range data {
		if key == "" || len(key) > maxPolicyEventField {
			return fmt.Errorf("%w: invalid policy event field", model.ErrInvalid)
		}
		if value == nil {
			return fmt.Errorf("%w: nil policy event field", model.ErrInvalid)
		}
		switch typed := value.(type) {
		case string:
			if len(typed) == 0 || len(typed) > maxPolicyEventField {
				return fmt.Errorf("%w: bounded policy event field required", model.ErrInvalid)
			}
		case int64, uint64, bool:
		default:
			return fmt.Errorf("%w: policy event fields must be scalar", model.ErrInvalid)
		}
	}
	if result, ok := data["result"].(string); !ok || (result != "allowed" && result != "denied") {
		return fmt.Errorf("%w: unknown policy event result", model.ErrInvalid)
	}
	if reason, ok := data["reason_code"].(string); !ok || (reason != string(policy.CodeAuthorizationAllowed) && reason != string(policy.CodeAuthorizationDenied)) {
		return fmt.Errorf("%w: unknown policy event reason", model.ErrInvalid)
	}
	return nil
}

func policyEventID(eventType policy.PolicyEventType, data map[string]any) (string, []byte, error) {
	payload, err := json.Marshal(data)
	if err != nil {
		return "", nil, fmt.Errorf("%w: encode policy event", model.ErrInvalid)
	}
	sum := sha256.Sum256(append([]byte(string(eventType)+"\x00"), payload...))
	return "POLICY-EVENT-" + hex.EncodeToString(sum[:]), payload, nil
}

// appendPolicyEvent writes a bounded, typed policy fact inside the caller's
// transaction. The deterministic event ID makes exact retries idempotent.
func (s *Store) appendPolicyEvent(ctx context.Context, tx *sql.Tx, eventType policy.PolicyEventType, request policy.PolicyMutationRequest, result, reason string) error {
	if !eventType.Valid() {
		return fmt.Errorf("%w: unknown policy event type", model.ErrInvalid)
	}
	if err := request.Validate(); err != nil {
		return err
	}
	data := policyEventData(request, result, reason)
	if err := validatePolicyEventData(data); err != nil {
		return err
	}
	eventID, payload, err := policyEventID(eventType, data)
	if err != nil {
		return err
	}
	timestamp := time.Now().UTC()
	var storedType, storedData, storedTimestamp string
	var projectID, taskID, actorID, sessionID sql.NullString
	var revision int64
	err = tx.QueryRowContext(ctx, `
		SELECT event_type, project_id, task_id, actor_agent_id, session_id,
		       aggregate_revision, timestamp, data_json
		FROM audit_events WHERE event_id = ?
	`, eventID).Scan(&storedType, &projectID, &taskID, &actorID, &sessionID,
		&revision, &storedTimestamp, &storedData)
	if err == nil {
		if storedType == string(eventType) && storedData == string(payload) && revision == 0 &&
			!projectID.Valid && !taskID.Valid && !actorID.Valid && !sessionID.Valid {
			return nil
		}
		return fmt.Errorf("%w: policy event identity conflict", model.ErrConflict)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read policy event: %w", err)
	}
	return s.AppendEvent(ctx, tx, model.Event{
		ID: eventID, Type: string(eventType), Timestamp: timestamp,
		AggregateRevision: 0, Data: data,
	})
}

func (s *Store) recordPolicyDeniedEvent(ctx context.Context, request policy.PolicyMutationRequest) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin policy denial event: %w", err)
	}
	defer tx.Rollback()
	if err := s.appendPolicyEvent(ctx, tx, policy.EventPolicyDecisionDenied, request, "denied", string(policy.CodeAuthorizationDenied)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit policy denial event: %w", err)
	}
	return nil
}
