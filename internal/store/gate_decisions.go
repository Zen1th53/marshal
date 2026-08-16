package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/Zen1th53/marshal/internal/gate"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policy"
)

func (s *Store) PutGateDecision(ctx context.Context, decision gate.Decision) error {
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
		(decision_id, gate_point, subject, resource, allowed, checks_json, policy_ids_json, policy_digest, change_digest, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, decision.DecisionID, decision.Point, decision.Subject, decision.Resource,
		decision.Allowed, string(checks), string(policyIDs), decision.PolicyDigest, decision.ChangeDigest, decision.CreatedAt.UTC().Format(time.RFC3339Nano))
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
	var point, checks, policyIDs, policyDigest, createdAt, changeDigest string
	var allowed int
	err := s.db.QueryRowContext(ctx, `SELECT decision_id, gate_point, subject, resource, allowed, checks_json, policy_ids_json, policy_digest, change_digest, created_at FROM gate_decisions WHERE decision_id = ?`, id).
		Scan(&decision.DecisionID, &point, &decision.Subject, &decision.Resource, &allowed, &checks, &policyIDs, &policyDigest, &changeDigest, &createdAt)
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
