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

// SaveGoalContract persists a GoalContract revision under CAS concurrency control.
// If expectedRevision == 0, it creates revision 1 as the active goal for the session.
// If expectedRevision > 0, it ensures the current active revision matches expectedRevision,
// then inserts revision = expectedRevision + 1 and advances the active pointer.
func (s *Store) SaveGoalContract(ctx context.Context, goal model.GoalContract, expectedRevision int64) error {
	if expectedRevision == 0 {
		if goal.Revision < 1 {
			goal.Revision = 1
		}
	} else {
		if goal.Revision <= expectedRevision {
			goal.Revision = expectedRevision + 1
		}
	}
	if goal.UnderstandingState == "" {
		goal.EvaluateUnderstanding()
	}
	if err := model.ValidateGoal(goal); err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin save goal tx: %w", err)
	}
	defer tx.Rollback()

	// Check active goal for session
	var currentGoalID string
	var currentRevision int64
	err = tx.QueryRowContext(ctx, `
		SELECT active_goal_id, active_revision
		FROM goal_active
		WHERE session_id = ?
	`, goal.SessionID).Scan(&currentGoalID, &currentRevision)

	if expectedRevision == 0 {
		if err == nil {
			return fmt.Errorf("%w: session %s already has active goal %s at revision %d",
				model.ErrGoalConflict, goal.SessionID, currentGoalID, currentRevision)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("check active goal: %w", err)
		}
		goal.Revision = 1
	} else {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: session %s has no active goal to update",
				model.ErrGoalNotFound, goal.SessionID)
		} else if err != nil {
			return fmt.Errorf("check active goal: %w", err)
		}
		if currentGoalID != goal.ID || currentRevision != expectedRevision {
			return fmt.Errorf("%w: current revision is %d (goal %s), expected %d (goal %s)",
				model.ErrGoalConflict, currentRevision, currentGoalID, expectedRevision, goal.ID)
		}
		goal.Revision = expectedRevision + 1
	}

	scopeJSON, err := json.Marshal(goal.Scope)
	if err != nil {
		return fmt.Errorf("marshal scope: %w", err)
	}
	constraintsJSON, err := json.Marshal(goal.Constraints)
	if err != nil {
		return fmt.Errorf("marshal constraints: %w", err)
	}
	doNotDoJSON, err := json.Marshal(goal.DoNotDo)
	if err != nil {
		return fmt.Errorf("marshal do_not_do: %w", err)
	}
	successCriteriaJSON, err := json.Marshal(goal.SuccessCriteria)
	if err != nil {
		return fmt.Errorf("marshal success_criteria: %w", err)
	}
	criticalClaimsJSON, err := json.Marshal(goal.RequiredCriticalClaims)
	if err != nil {
		return fmt.Errorf("marshal critical_claims: %w", err)
	}
	unresolvedDecisionsJSON, err := json.Marshal(goal.UnresolvedDecisions)
	if err != nil {
		return fmt.Errorf("marshal unresolved_decisions: %w", err)
	}
	assumptionsJSON, err := json.Marshal(goal.Assumptions)
	if err != nil {
		return fmt.Errorf("marshal assumptions: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if goal.CreatedAt.IsZero() {
		goal.CreatedAt = time.Now().UTC()
	}
	goal.UpdatedAt = time.Now().UTC()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO goal_contracts(
			goal_id, session_id, revision, desired_outcome, expected_artifact,
			scope_json, constraints_json, do_not_do_json, success_criteria_json,
			risk, authority_source, budget_ref, critical_claims_json,
			understanding_state, unresolved_decisions_json, assumptions_json,
			repo_commit, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		goal.ID,
		goal.SessionID,
		goal.Revision,
		goal.DesiredOutcome,
		goal.ExpectedArtifact,
		string(scopeJSON),
		string(constraintsJSON),
		string(doNotDoJSON),
		string(successCriteriaJSON),
		string(goal.Risk),
		goal.AuthoritySource,
		goal.BudgetRef,
		string(criticalClaimsJSON),
		string(goal.UnderstandingState),
		string(unresolvedDecisionsJSON),
		string(assumptionsJSON),
		goal.RepoCommit,
		goal.CreatedAt.Format(time.RFC3339Nano),
		goal.UpdatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert goal contract: %w", err)
	}

	// Update or insert into goal_active
	_, err = tx.ExecContext(ctx, `
		INSERT INTO goal_active(session_id, active_goal_id, active_revision, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			active_goal_id = excluded.active_goal_id,
			active_revision = excluded.active_revision,
			updated_at = excluded.updated_at
	`, goal.SessionID, goal.ID, goal.Revision, now)
	if err != nil {
		return fmt.Errorf("update active goal: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit save goal: %w", err)
	}
	return nil
}

// GetGoalContract fetches a specific revision of a GoalContract.
func (s *Store) GetGoalContract(ctx context.Context, goalID string, revision int64) (model.GoalContract, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT
			goal_id, session_id, revision, desired_outcome, expected_artifact,
			scope_json, constraints_json, do_not_do_json, success_criteria_json,
			risk, authority_source, budget_ref, critical_claims_json,
			understanding_state, unresolved_decisions_json, assumptions_json,
			repo_commit, created_at, updated_at
		FROM goal_contracts
		WHERE goal_id = ? AND revision = ?
	`, goalID, revision)

	return scanGoalContract(row)
}

// GetActiveGoalContract returns the current active revision for a session.
func (s *Store) GetActiveGoalContract(ctx context.Context, sessionID string) (model.GoalContract, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT
			g.goal_id, g.session_id, g.revision, g.desired_outcome, g.expected_artifact,
			g.scope_json, g.constraints_json, g.do_not_do_json, g.success_criteria_json,
			g.risk, g.authority_source, g.budget_ref, g.critical_claims_json,
			g.understanding_state, g.unresolved_decisions_json, g.assumptions_json,
			g.repo_commit, g.created_at, g.updated_at
		FROM goal_active a
		JOIN goal_contracts g ON a.active_goal_id = g.goal_id AND a.active_revision = g.revision
		WHERE a.session_id = ?
	`, sessionID)

	return scanGoalContract(row)
}

// ListGoalRevisions returns all revisions for a goal in chronological order.
func (s *Store) ListGoalRevisions(ctx context.Context, goalID string) ([]model.GoalContract, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			goal_id, session_id, revision, desired_outcome, expected_artifact,
			scope_json, constraints_json, do_not_do_json, success_criteria_json,
			risk, authority_source, budget_ref, critical_claims_json,
			understanding_state, unresolved_decisions_json, assumptions_json,
			repo_commit, created_at, updated_at
		FROM goal_contracts
		WHERE goal_id = ?
		ORDER BY revision ASC
	`, goalID)
	if err != nil {
		return nil, fmt.Errorf("list goal revisions query: %w", err)
	}
	defer rows.Close()

	var list []model.GoalContract
	for rows.Next() {
		g, err := scanGoalContract(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return list, nil
}

// SetActiveGoalContract points the active goal pointer to a specific historical revision.
func (s *Store) SetActiveGoalContract(ctx context.Context, sessionID string, goalID string, revision int64) error {
	// Verify revision exists
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM goal_contracts WHERE goal_id = ? AND revision = ?
	`, goalID, revision).Scan(&count)
	if err != nil {
		return fmt.Errorf("check revision exists: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("%w: revision %d for goal %s does not exist", model.ErrGoalNotFound, revision, goalID)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO goal_active(session_id, active_goal_id, active_revision, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			active_goal_id = excluded.active_goal_id,
			active_revision = excluded.active_revision,
			updated_at = excluded.updated_at
	`, sessionID, goalID, revision, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("set active goal: %w", err)
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanGoalContract(r rowScanner) (model.GoalContract, error) {
	var g model.GoalContract
	var (
		scopeJSON, constraintsJSON, doNotDoJSON, successCriteriaJSON string
		criticalClaimsJSON, unresolvedDecisionsJSON, assumptionsJSON string
		riskStr, stateStr, createdAtStr, updatedAtStr                string
	)

	err := r.Scan(
		&g.ID,
		&g.SessionID,
		&g.Revision,
		&g.DesiredOutcome,
		&g.ExpectedArtifact,
		&scopeJSON,
		&constraintsJSON,
		&doNotDoJSON,
		&successCriteriaJSON,
		&riskStr,
		&g.AuthoritySource,
		&g.BudgetRef,
		&criticalClaimsJSON,
		&stateStr,
		&unresolvedDecisionsJSON,
		&assumptionsJSON,
		&g.RepoCommit,
		&createdAtStr,
		&updatedAtStr,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.GoalContract{}, model.ErrGoalNotFound
		}
		return model.GoalContract{}, fmt.Errorf("scan goal contract: %w", err)
	}

	g.Risk = model.Risk(riskStr)
	g.UnderstandingState = model.UnderstandingState(stateStr)

	if err := json.Unmarshal([]byte(scopeJSON), &g.Scope); err != nil {
		return model.GoalContract{}, fmt.Errorf("unmarshal scope: %w", err)
	}
	if err := json.Unmarshal([]byte(constraintsJSON), &g.Constraints); err != nil {
		return model.GoalContract{}, fmt.Errorf("unmarshal constraints: %w", err)
	}
	if err := json.Unmarshal([]byte(doNotDoJSON), &g.DoNotDo); err != nil {
		return model.GoalContract{}, fmt.Errorf("unmarshal do_not_do: %w", err)
	}
	if err := json.Unmarshal([]byte(successCriteriaJSON), &g.SuccessCriteria); err != nil {
		return model.GoalContract{}, fmt.Errorf("unmarshal success_criteria: %w", err)
	}
	if err := json.Unmarshal([]byte(criticalClaimsJSON), &g.RequiredCriticalClaims); err != nil {
		return model.GoalContract{}, fmt.Errorf("unmarshal critical_claims: %w", err)
	}
	if err := json.Unmarshal([]byte(unresolvedDecisionsJSON), &g.UnresolvedDecisions); err != nil {
		return model.GoalContract{}, fmt.Errorf("unmarshal unresolved_decisions: %w", err)
	}
	if err := json.Unmarshal([]byte(assumptionsJSON), &g.Assumptions); err != nil {
		return model.GoalContract{}, fmt.Errorf("unmarshal assumptions: %w", err)
	}

	t, err := time.Parse(time.RFC3339Nano, createdAtStr)
	if err == nil {
		g.CreatedAt = t
	}
	t, err = time.Parse(time.RFC3339Nano, updatedAtStr)
	if err == nil {
		g.UpdatedAt = t
	}

	return g, nil
}
