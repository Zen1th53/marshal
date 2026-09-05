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

// SaveInterpretation persists an agent's independent interpretation to the database.
func (s *Store) SaveInterpretation(ctx context.Context, interp model.Interpretation) error {
	if err := interp.Validate(); err != nil {
		return fmt.Errorf("%w: %v", model.ErrInvalid, err)
	}

	scopeJSON, err := json.Marshal(interp.Scope)
	if err != nil {
		return fmt.Errorf("%w: marshal scope: %v", model.ErrInvalid, err)
	}
	constraintsJSON, err := json.Marshal(interp.Constraints)
	if err != nil {
		return fmt.Errorf("%w: marshal constraints: %v", model.ErrInvalid, err)
	}
	assumptionsJSON, err := json.Marshal(interp.Assumptions)
	if err != nil {
		return fmt.Errorf("%w: marshal assumptions: %v", model.ErrInvalid, err)
	}

	payload := map[string]any{
		"identified_risks": interp.IdentifiedRisks,
		"success_criteria": interp.SuccessCriteria,
		"ambiguities":      interp.Ambiguities,
		"session_id":       interp.Author.SessionID,
		"run_id":           interp.Author.RunID,
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("%w: marshal payload: %v", model.ErrInvalid, err)
	}

	for attempt := 0; ; attempt++ {
		err = s.saveInterpretationTx(ctx, interp, string(scopeJSON), string(constraintsJSON), string(assumptionsJSON), string(payloadJSON))
		if !isSQLiteBusy(err) || attempt >= sqliteBusyRetries {
			return err
		}
		s.observeContention()
		if err := waitSQLiteRetry(ctx, attempt); err != nil {
			return err
		}
	}
}

func (s *Store) saveInterpretationTx(
	ctx context.Context,
	interp model.Interpretation,
	scopeJSON, constraintsJSON, assumptionsJSON, payloadJSON string,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin save interpretation: %w", err)
	}
	defer tx.Rollback()

	submittedAt := interp.SubmittedAt.UTC().Format(time.RFC3339Nano)
	isDestructive := 0
	if interp.IsDestructive {
		isDestructive = 1
	}

	query := `
		INSERT INTO blind_interpretations (
			interpretation_id, goal_id, goal_revision, session_id,
			agent_id, harness, model, desired_outcome, expected_artifact,
			scope_json, constraints_json, assumptions_json, is_destructive,
			payload_json, submitted_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(interpretation_id) DO UPDATE SET
			desired_outcome = excluded.desired_outcome,
			expected_artifact = excluded.expected_artifact,
			scope_json = excluded.scope_json,
			constraints_json = excluded.constraints_json,
			assumptions_json = excluded.assumptions_json,
			is_destructive = excluded.is_destructive,
			payload_json = excluded.payload_json,
			submitted_at = excluded.submitted_at
	`
	if _, err := tx.ExecContext(ctx, query,
		interp.ID,
		interp.GoalID,
		interp.GoalRevision,
		interp.SessionID,
		interp.Author.AgentID,
		interp.Author.Harness,
		interp.Author.Model,
		interp.DesiredOutcome,
		interp.ExpectedArtifact,
		scopeJSON,
		constraintsJSON,
		assumptionsJSON,
		isDestructive,
		payloadJSON,
		submittedAt,
	); err != nil {
		return fmt.Errorf("insert blind interpretation: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit save interpretation: %w", err)
	}
	return nil
}

// ListInterpretations retrieves all blind interpretations recorded for a goal revision.
func (s *Store) ListInterpretations(ctx context.Context, goalID string, revision int64) ([]model.Interpretation, error) {
	query := `
		SELECT
			interpretation_id, goal_id, goal_revision, session_id,
			agent_id, harness, model, desired_outcome, expected_artifact,
			scope_json, constraints_json, assumptions_json, is_destructive,
			payload_json, submitted_at
		FROM blind_interpretations
		WHERE goal_id = ? AND goal_revision = ?
		ORDER BY submitted_at ASC
	`
	rows, err := s.db.QueryContext(ctx, query, goalID, revision)
	if err != nil {
		return nil, fmt.Errorf("query blind interpretations: %w", err)
	}
	defer rows.Close()

	var result []model.Interpretation
	for rows.Next() {
		var (
			interp          model.Interpretation
			isDestructive   int
			scopeJSON       string
			constraintsJSON string
			assumptionsJSON string
			payloadJSON     string
			submittedAt     string
		)

		if err := rows.Scan(
			&interp.ID,
			&interp.GoalID,
			&interp.GoalRevision,
			&interp.SessionID,
			&interp.Author.AgentID,
			&interp.Author.Harness,
			&interp.Author.Model,
			&interp.DesiredOutcome,
			&interp.ExpectedArtifact,
			&scopeJSON,
			&constraintsJSON,
			&assumptionsJSON,
			&isDestructive,
			&payloadJSON,
			&submittedAt,
		); err != nil {
			return nil, fmt.Errorf("scan blind interpretation: %w", err)
		}

		interp.IsDestructive = (isDestructive == 1)
		if scopeJSON != "" && scopeJSON != "[]" {
			_ = json.Unmarshal([]byte(scopeJSON), &interp.Scope)
		}
		if constraintsJSON != "" && constraintsJSON != "[]" {
			_ = json.Unmarshal([]byte(constraintsJSON), &interp.Constraints)
		}
		if assumptionsJSON != "" && assumptionsJSON != "[]" {
			_ = json.Unmarshal([]byte(assumptionsJSON), &interp.Assumptions)
		}
		if payloadJSON != "" && payloadJSON != "{}" {
			var p struct {
				IdentifiedRisks []string `json:"identified_risks"`
				SuccessCriteria []string `json:"success_criteria"`
				Ambiguities     []string `json:"ambiguities"`
				SessionID       string   `json:"session_id"`
				RunID           string   `json:"run_id"`
			}
			if err := json.Unmarshal([]byte(payloadJSON), &p); err == nil {
				interp.IdentifiedRisks = p.IdentifiedRisks
				interp.SuccessCriteria = p.SuccessCriteria
				interp.Ambiguities = p.Ambiguities
				interp.Author.SessionID = p.SessionID
				interp.Author.RunID = p.RunID
			}
		}

		parsedTime, err := time.Parse(time.RFC3339Nano, submittedAt)
		if err != nil {
			parsedTime, err = time.Parse(time.RFC3339, submittedAt)
			if err != nil {
				return nil, fmt.Errorf("parse submitted_at: %w", err)
			}
		}
		interp.SubmittedAt = parsedTime
		result = append(result, interp)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate blind interpretations: %w", err)
	}
	return result, nil
}

// SaveInterpretationResolution persists the outcome of blind interpretation comparison.
func (s *Store) SaveInterpretationResolution(ctx context.Context, res model.InterpretationResolution) error {
	if err := res.Validate(); err != nil {
		return fmt.Errorf("%w: %v", model.ErrInvalid, err)
	}

	divergencesJSON, err := json.Marshal(res.Divergences)
	if err != nil {
		return fmt.Errorf("%w: marshal divergences: %v", model.ErrInvalid, err)
	}
	questionsJSON, err := json.Marshal(res.ConcreteQuestions)
	if err != nil {
		return fmt.Errorf("%w: marshal questions: %v", model.ErrInvalid, err)
	}

	for attempt := 0; ; attempt++ {
		err = s.saveInterpretationResolutionTx(ctx, res, string(divergencesJSON), string(questionsJSON))
		if !isSQLiteBusy(err) || attempt >= sqliteBusyRetries {
			return err
		}
		s.observeContention()
		if err := waitSQLiteRetry(ctx, attempt); err != nil {
			return err
		}
	}
}

func (s *Store) saveInterpretationResolutionTx(
	ctx context.Context,
	res model.InterpretationResolution,
	divergencesJSON, questionsJSON string,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin save interpretation resolution: %w", err)
	}
	defer tx.Rollback()

	resolvedAt := res.ResolvedAt.UTC().Format(time.RFC3339Nano)

	query := `
		INSERT INTO interpretation_resolutions (
			resolution_id, goal_id, goal_revision, session_id,
			state, required_count, collected_count, consensus_confirmed,
			divergences_json, questions_json, message, resolved_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id, goal_id, goal_revision) DO UPDATE SET
			state = excluded.state,
			required_count = excluded.required_count,
			collected_count = excluded.collected_count,
			consensus_confirmed = excluded.consensus_confirmed,
			divergences_json = excluded.divergences_json,
			questions_json = excluded.questions_json,
			message = excluded.message,
			resolved_at = excluded.resolved_at
	`
	consensusConfirmed := 0
	if res.ConsensusConfirmed {
		consensusConfirmed = 1
	}

	if _, err := tx.ExecContext(ctx, query,
		res.ID,
		res.GoalID,
		res.GoalRevision,
		res.SessionID,
		string(res.State),
		res.RequiredCount,
		res.CollectedCount,
		consensusConfirmed,
		divergencesJSON,
		questionsJSON,
		res.Message,
		resolvedAt,
	); err != nil {
		return fmt.Errorf("insert interpretation resolution: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit save interpretation resolution: %w", err)
	}
	return nil
}

// GetInterpretationResolution fetches the resolution record for a goal revision.
func (s *Store) GetInterpretationResolution(ctx context.Context, sessionID, goalID string, revision int64) (*model.InterpretationResolution, error) {
	query := `
		SELECT
			resolution_id, goal_id, goal_revision, session_id,
			state, required_count, collected_count, consensus_confirmed,
			divergences_json, questions_json, message, resolved_at
		FROM interpretation_resolutions
		WHERE session_id = ? AND goal_id = ? AND goal_revision = ?
	`
	row := s.db.QueryRowContext(ctx, query, sessionID, goalID, revision)

	var (
		res                model.InterpretationResolution
		stateStr           string
		consensusConfirmed int
		divergencesJSON    string
		questionsJSON      string
		resolvedAt         string
	)

	err := row.Scan(
		&res.ID,
		&res.GoalID,
		&res.GoalRevision,
		&res.SessionID,
		&stateStr,
		&res.RequiredCount,
		&res.CollectedCount,
		&consensusConfirmed,
		&divergencesJSON,
		&questionsJSON,
		&res.Message,
		&resolvedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: interpretation resolution not found", model.ErrNotFound)
		}
		return nil, fmt.Errorf("query interpretation resolution: %w", err)
	}

	res.State = model.UnderstandingState(stateStr)
	res.ConsensusConfirmed = (consensusConfirmed == 1)
	if divergencesJSON != "" && divergencesJSON != "[]" {
		_ = json.Unmarshal([]byte(divergencesJSON), &res.Divergences)
	}
	if questionsJSON != "" && questionsJSON != "[]" {
		_ = json.Unmarshal([]byte(questionsJSON), &res.ConcreteQuestions)
	}

	parsedTime, err := time.Parse(time.RFC3339Nano, resolvedAt)
	if err != nil {
		parsedTime, err = time.Parse(time.RFC3339, resolvedAt)
		if err != nil {
			return nil, fmt.Errorf("parse resolved_at: %w", err)
		}
	}
	res.ResolvedAt = parsedTime

	return &res, nil
}
