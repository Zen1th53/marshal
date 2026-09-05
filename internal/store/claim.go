package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// SaveClaim writes or updates a claim record in SQLite.
func (s *Store) SaveClaim(ctx context.Context, claim model.Claim) error {
	if err := claim.Validate(); err != nil {
		return fmt.Errorf("%w: %v", model.ErrInvalid, err)
	}

	authorJSON, err := json.Marshal(claim.Author)
	if err != nil {
		return fmt.Errorf("%w: marshal author: %v", model.ErrInvalid, err)
	}
	supportingJSON, err := json.Marshal(claim.SupportingEvidence)
	if err != nil {
		return fmt.Errorf("%w: marshal supporting evidence: %v", model.ErrInvalid, err)
	}
	contradictingJSON, err := json.Marshal(claim.ContradictingEvidence)
	if err != nil {
		return fmt.Errorf("%w: marshal contradicting evidence: %v", model.ErrInvalid, err)
	}
	clustersJSON, err := json.Marshal(claim.SourceClusters)
	if err != nil {
		return fmt.Errorf("%w: marshal source clusters: %v", model.ErrInvalid, err)
	}
	bindingJSON, err := json.Marshal(claim.Binding)
	if err != nil {
		return fmt.Errorf("%w: marshal binding: %v", model.ErrInvalid, err)
	}

	for attempt := 0; ; attempt++ {
		err = s.saveClaimTx(ctx, claim, string(authorJSON), string(supportingJSON),
			string(contradictingJSON), string(clustersJSON), string(bindingJSON))
		if !isSQLiteBusy(err) || attempt >= sqliteBusyRetries {
			return err
		}
		s.observeContention()
		if err := waitSQLiteRetry(ctx, attempt); err != nil {
			return err
		}
	}
}

func (s *Store) saveClaimTx(
	ctx context.Context,
	claim model.Claim,
	authorJSON, supportingJSON, contradictingJSON, clustersJSON, bindingJSON string,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin save claim: %w", err)
	}
	defer tx.Rollback()

	createdAt := claim.CreatedAt.UTC().Format(time.RFC3339Nano)
	updatedAt := claim.UpdatedAt.UTC().Format(time.RFC3339Nano)
	evaluatedAt := claim.EvaluatedAt.UTC().Format(time.RFC3339Nano)

	query := `
		INSERT INTO claims (
			claim_id, goal_id, goal_revision, subject, normalized_text, scope,
			criticality, state, predecessor_id, supersedes_id, author_provenance_json,
			supporting_evidence_json, contradicting_evidence_json, source_clusters_json,
			binding_json, state_reason, created_at, updated_at, evaluated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(claim_id) DO UPDATE SET
			goal_id = excluded.goal_id,
			goal_revision = excluded.goal_revision,
			subject = excluded.subject,
			normalized_text = excluded.normalized_text,
			scope = excluded.scope,
			criticality = excluded.criticality,
			state = excluded.state,
			predecessor_id = excluded.predecessor_id,
			supersedes_id = excluded.supersedes_id,
			author_provenance_json = excluded.author_provenance_json,
			supporting_evidence_json = excluded.supporting_evidence_json,
			contradicting_evidence_json = excluded.contradicting_evidence_json,
			source_clusters_json = excluded.source_clusters_json,
			binding_json = excluded.binding_json,
			state_reason = excluded.state_reason,
			updated_at = excluded.updated_at,
			evaluated_at = excluded.evaluated_at
	`

	_, err = tx.ExecContext(ctx, query,
		claim.ID, claim.GoalID, claim.GoalRevision, claim.Subject, claim.NormalizedText,
		claim.Scope, string(claim.Criticality), string(claim.State), claim.PredecessorID,
		claim.SupersedesID, authorJSON, supportingJSON, contradictingJSON, clustersJSON,
		bindingJSON, claim.StateReason, createdAt, updatedAt, evaluatedAt)
	if err != nil {
		return fmt.Errorf("upsert claim: %w", err)
	}

	return tx.Commit()
}

// GetClaim retrieves a claim by ID.
func (s *Store) GetClaim(ctx context.Context, claimID string) (model.Claim, error) {
	query := `
		SELECT claim_id, goal_id, goal_revision, subject, normalized_text, scope,
		       criticality, state, predecessor_id, supersedes_id, author_provenance_json,
		       supporting_evidence_json, contradicting_evidence_json, source_clusters_json,
		       binding_json, state_reason, created_at, updated_at, evaluated_at
		FROM claims WHERE claim_id = ?
	`
	row := s.db.QueryRowContext(ctx, query, claimID)
	return scanClaim(row)
}

// ListClaimsByGoal returns all claims for a given goal ID and revision.
func (s *Store) ListClaimsByGoal(ctx context.Context, goalID string, goalRevision int64) ([]model.Claim, error) {
	query := `
		SELECT claim_id, goal_id, goal_revision, subject, normalized_text, scope,
		       criticality, state, predecessor_id, supersedes_id, author_provenance_json,
		       supporting_evidence_json, contradicting_evidence_json, source_clusters_json,
		       binding_json, state_reason, created_at, updated_at, evaluated_at
		FROM claims WHERE goal_id = ? AND goal_revision = ?
		ORDER BY created_at ASC
	`
	rows, err := s.db.QueryContext(ctx, query, goalID, goalRevision)
	if err != nil {
		return nil, fmt.Errorf("list claims by goal: %w", err)
	}
	defer rows.Close()

	var claims []model.Claim
	for rows.Next() {
		claim, err := scanClaim(rows)
		if err != nil {
			return nil, err
		}
		claims = append(claims, claim)
	}
	return claims, rows.Err()
}

// ListClaimsByScope returns all claims for a given scope.
func (s *Store) ListClaimsByScope(ctx context.Context, scope string) ([]model.Claim, error) {
	query := `
		SELECT claim_id, goal_id, goal_revision, subject, normalized_text, scope,
		       criticality, state, predecessor_id, supersedes_id, author_provenance_json,
		       supporting_evidence_json, contradicting_evidence_json, source_clusters_json,
		       binding_json, state_reason, created_at, updated_at, evaluated_at
		FROM claims WHERE scope = ?
		ORDER BY created_at ASC
	`
	rows, err := s.db.QueryContext(ctx, query, scope)
	if err != nil {
		return nil, fmt.Errorf("list claims by scope: %w", err)
	}
	defer rows.Close()

	var claims []model.Claim
	for rows.Next() {
		claim, err := scanClaim(rows)
		if err != nil {
			return nil, err
		}
		claims = append(claims, claim)
	}
	return claims, rows.Err()
}

// RecordClaimTransition persists an immutable transition entry.
func (s *Store) RecordClaimTransition(ctx context.Context, trans model.ClaimTransition) error {
	if err := trans.Validate(); err != nil {
		return fmt.Errorf("%w: %v", model.ErrInvalid, err)
	}

	actorJSON, err := json.Marshal(trans.Actor)
	if err != nil {
		return fmt.Errorf("%w: marshal actor: %v", model.ErrInvalid, err)
	}

	evidenceJSON := "{}"
	if trans.EvidenceRef != nil {
		data, err := json.Marshal(trans.EvidenceRef)
		if err != nil {
			return fmt.Errorf("%w: marshal evidence ref: %v", model.ErrInvalid, err)
		}
		evidenceJSON = string(data)
	}

	timestamp := trans.Timestamp.UTC().Format(time.RFC3339Nano)

	query := `
		INSERT INTO claim_transitions (
			transition_id, claim_id, from_state, to_state, reason,
			actor_provenance_json, evidence_ref_json, timestamp
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err = s.db.ExecContext(ctx, query,
		trans.TransitionID, trans.ClaimID, string(trans.FromState), string(trans.ToState),
		trans.Reason, string(actorJSON), evidenceJSON, timestamp)
	if err != nil {
		return fmt.Errorf("record claim transition: %w", err)
	}

	return nil
}

// GetClaimTransitions retrieves all transitions for a claim ordered chronologically.
func (s *Store) GetClaimTransitions(ctx context.Context, claimID string) ([]model.ClaimTransition, error) {
	query := `
		SELECT transition_id, claim_id, from_state, to_state, reason,
		       actor_provenance_json, evidence_ref_json, timestamp
		FROM claim_transitions
		WHERE claim_id = ?
		ORDER BY timestamp ASC
	`
	rows, err := s.db.QueryContext(ctx, query, claimID)
	if err != nil {
		return nil, fmt.Errorf("get claim transitions: %w", err)
	}
	defer rows.Close()

	var transitions []model.ClaimTransition
	for rows.Next() {
		var t model.ClaimTransition
		var fromState, toState, actorJSON, evJSON, ts string
		if err := rows.Scan(&t.TransitionID, &t.ClaimID, &fromState, &toState, &t.Reason,
			&actorJSON, &evJSON, &ts); err != nil {
			return nil, fmt.Errorf("scan claim transition: %w", err)
		}
		t.FromState = model.ClaimState(fromState)
		t.ToState = model.ClaimState(toState)
		if parsed, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			t.Timestamp = parsed
		}
		if err := json.Unmarshal([]byte(actorJSON), &t.Actor); err != nil {
			return nil, fmt.Errorf("decode transition actor: %w", err)
		}
		if evJSON != "{}" && evJSON != "" {
			var ev model.EvidenceRef
			if err := json.Unmarshal([]byte(evJSON), &ev); err == nil {
				t.EvidenceRef = &ev
			}
		}
		transitions = append(transitions, t)
	}
	return transitions, rows.Err()
}

type claimScanner interface {
	Scan(dest ...any) error
}

func scanClaim(scanner claimScanner) (model.Claim, error) {
	var c model.Claim
	var critStr, stateStr, authorJSON, suppJSON, contraJSON, clustJSON, bindJSON string
	var createdStr, updatedStr, evalStr string

	err := scanner.Scan(
		&c.ID, &c.GoalID, &c.GoalRevision, &c.Subject, &c.NormalizedText, &c.Scope,
		&critStr, &stateStr, &c.PredecessorID, &c.SupersedesID, &authorJSON,
		&suppJSON, &contraJSON, &clustJSON, &bindJSON, &c.StateReason,
		&createdStr, &updatedStr, &evalStr,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Claim{}, model.ErrNotFound
	}
	if err != nil {
		return model.Claim{}, fmt.Errorf("scan claim: %w", err)
	}

	c.Criticality = model.ClaimCriticality(critStr)
	c.State = model.ClaimState(stateStr)

	if parsed, err := time.Parse(time.RFC3339Nano, createdStr); err == nil {
		c.CreatedAt = parsed
	}
	if parsed, err := time.Parse(time.RFC3339Nano, updatedStr); err == nil {
		c.UpdatedAt = parsed
	}
	if parsed, err := time.Parse(time.RFC3339Nano, evalStr); err == nil {
		c.EvaluatedAt = parsed
	}

	if err := json.Unmarshal([]byte(authorJSON), &c.Author); err != nil {
		return model.Claim{}, fmt.Errorf("decode author: %w", err)
	}
	if err := json.Unmarshal([]byte(suppJSON), &c.SupportingEvidence); err != nil {
		return model.Claim{}, fmt.Errorf("decode supporting evidence: %w", err)
	}
	if err := json.Unmarshal([]byte(contraJSON), &c.ContradictingEvidence); err != nil {
		return model.Claim{}, fmt.Errorf("decode contradicting evidence: %w", err)
	}
	if err := json.Unmarshal([]byte(clustJSON), &c.SourceClusters); err != nil {
		return model.Claim{}, fmt.Errorf("decode source clusters: %w", err)
	}
	if err := json.Unmarshal([]byte(bindJSON), &c.Binding); err != nil {
		return model.Claim{}, fmt.Errorf("decode binding: %w", err)
	}

	return c, nil
}
