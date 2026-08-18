package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/gate"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policy"
)

var ErrStaleGateDecision = errors.New("gate decision is stale")

func (s *Store) PutGateDecision(ctx context.Context, decision gate.Decision) error {
	if decision.State == "" {
		decision.State = gate.DecisionStateRequested
	}
	if err := decision.Validate(); err != nil {
		return err
	}
	checks, err := json.Marshal(decision.Checks)
	if err != nil {
		return fmt.Errorf("encode gate checks: %w", err)
	}
	policyIDs, err := json.Marshal(decision.PolicyIDs)
	if err != nil {
		return fmt.Errorf("encode gate policy IDs: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO gate_decisions
		(decision_id, gate_point, subject, resource, allowed, checks_json, policy_ids_json, policy_digest, change_digest, created_at, state)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, decision.DecisionID, decision.Point, decision.Subject, decision.Resource,
		decision.Allowed, string(checks), string(policyIDs), decision.PolicyDigest, decision.ChangeDigest, decision.CreatedAt.UTC().Format(time.RFC3339Nano), decision.State)
	if err == nil {
		return nil
	}
	existing, getErr := s.GetGateDecision(ctx, decision.DecisionID)
	if getErr == nil && reflect.DeepEqual(existing, decision) {
		return nil
	}
	if getErr == nil {
		return fmt.Errorf("%w: gate decision is immutable", model.ErrConflict)
	}
	return fmt.Errorf("persist gate decision: %w", err)
}

func (s *Store) GetGateDecision(ctx context.Context, id string) (gate.Decision, error) {
	var decision gate.Decision
	var point, checks, policyIDs, policyDigest, createdAt, changeDigest, state string
	var allowed int
	err := s.db.QueryRowContext(ctx, `SELECT decision_id, gate_point, subject, resource, allowed, checks_json, policy_ids_json, policy_digest, change_digest, created_at, state FROM gate_decisions WHERE decision_id = ?`, id).
		Scan(&decision.DecisionID, &point, &decision.Subject, &decision.Resource, &allowed, &checks, &policyIDs, &policyDigest, &changeDigest, &createdAt, &state)
	if err == sql.ErrNoRows {
		return gate.Decision{}, fmt.Errorf("%w: gate decision not found", model.ErrNotFound)
	}
	if err != nil {
		return gate.Decision{}, fmt.Errorf("read gate decision: %w", err)
	}
	decision.Point = gate.GatePoint(point)
	decision.Allowed = allowed != 0
	decision.PolicyDigest = policy.PolicyDigest(policyDigest)
	decision.ChangeDigest = changeDigest
	decision.State = gate.DecisionState(state)
	if err := json.Unmarshal([]byte(checks), &decision.Checks); err != nil {
		return gate.Decision{}, fmt.Errorf("%w: invalid gate checks", model.ErrInvalid)
	}
	if err := json.Unmarshal([]byte(policyIDs), &decision.PolicyIDs); err != nil {
		return gate.Decision{}, fmt.Errorf("%w: invalid gate policy IDs", model.ErrInvalid)
	}
	parsed, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return gate.Decision{}, fmt.Errorf("%w: invalid gate decision time", model.ErrInvalid)
	}
	decision.CreatedAt = parsed
	if err := decision.Validate(); err != nil {
		return gate.Decision{}, fmt.Errorf("%w: invalid durable gate decision", model.ErrInvalid)
	}
	return decision, nil
}

// GetGateDecisionForPolicy revalidates the immutable policy binding at the
// privileged consumption boundary; a prior ALLOW is not ambient authority.
func (s *Store) GetGateDecisionForPolicy(ctx context.Context, id string, current policy.PolicyDigest) (gate.Decision, error) {
	if err := current.Validate(); err != nil {
		return gate.Decision{}, fmt.Errorf("%w: current policy digest is invalid", model.ErrInvalid)
	}
	decision, err := s.GetGateDecision(ctx, id)
	if err != nil {
		return gate.Decision{}, err
	}
	if decision.PolicyDigest != current {
		return gate.Decision{}, fmt.Errorf("%w: policy digest changed", ErrStaleGateDecision)
	}
	return decision, nil
}

func (s *Store) TransitionGateDecision(ctx context.Context, id, actor string, from, to gate.DecisionState) (gate.Decision, error) {
	if actor == "" || !gate.ValidDecisionTransition(from, to) {
		return gate.Decision{}, fmt.Errorf("%w: invalid gate transition", model.ErrConflict)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE gate_decisions SET state = ? WHERE decision_id = ? AND state = ?`, to, id, from)
	if err != nil {
		return gate.Decision{}, fmt.Errorf("transition gate decision: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return gate.Decision{}, err
	}
	if rows != 1 {
		return gate.Decision{}, fmt.Errorf("%w: stale gate decision state", model.ErrConflict)
	}
	return s.GetGateDecision(ctx, id)
}

// PutGateDecisionWithAudit commits canonical state before appending its
// bounded, idempotent audit projection. Event delivery is not authority.
func (s *Store) PutGateDecisionWithAudit(ctx context.Context, decision gate.Decision, eventStore events.Store) error {
	if err := s.PutGateDecision(ctx, decision); err != nil {
		return err
	}
	if eventStore == nil {
		return nil
	}
	key := gateDecisionEventKey(decision)
	eventType := events.EventTypeGateBlocked
	switch decision.State {
	case gate.DecisionStateAllowed:
		eventType = events.EventTypeGateAllowed
	case gate.DecisionStateDenied:
		eventType = events.EventTypeGateDenied
	case gate.DecisionStateInvalidated:
		eventType = events.EventTypeGateDecisionInvalidated
	case gate.DecisionStateConsumed:
		eventType = events.EventTypeGateDecisionConsumed
	}
	_, err := eventStore.Append(ctx, events.Event{
		ID: key, Type: eventType, Subject: decision.Subject,
		ResourceID: gateResourceReference(decision.Resource), At: decision.CreatedAt.UTC(),
		IdempotencyKey: key, Data: map[string]any{
			"decision_id": decision.DecisionID, "gate_point": string(decision.Point), "state": string(decision.State),
			"policy_digest": string(decision.PolicyDigest), "change_digest": decision.ChangeDigest,
		},
	})
	return err
}

func gateDecisionEventKey(decision gate.Decision) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{decision.DecisionID, string(decision.State)}, "\x00")))
	return "gate-event-" + hex.EncodeToString(sum[:])
}

func gateResourceReference(resource string) string {
	sum := sha256.Sum256([]byte(resource))
	return "gate-resource-" + hex.EncodeToString(sum[:])
}
