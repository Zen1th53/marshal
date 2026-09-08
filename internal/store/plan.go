package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
)

// This file persists execution plans.
//
// Two properties drive the design. Plan versions are immutable: a revision
// inserts a new row rather than updating one, so what was approved stays
// inspectable after the plan has moved on — an approval that cannot be shown
// later is not much of an audit trail. And writes are CAS-guarded on the
// version the writer read, so two surfaces revising the same plan cannot
// silently overwrite each other; the loser is told.

// SavePlan persists a plan version under CAS concurrency control.
//
// expectedVersion is the version the caller read. Zero creates the plan.
// A mismatch means someone else wrote in between, which is returned as
// plan.ErrPlanConflict rather than resolved: MARSHAL cannot know which of two
// concurrent intentions was meant to win, and picking one would silently
// discard the other.
func (s *Store) SavePlan(ctx context.Context, executionPlan plan.ExecutionPlan, expectedVersion int64) error {
	if executionPlan.ID == "" {
		return fmt.Errorf("%w: plan has no id", plan.ErrPlanInvalid)
	}
	if !executionPlan.ProjectID.Valid() {
		return fmt.Errorf("%w: plan names no project", plan.ErrPlanInvalid)
	}
	if executionPlan.Goal.GoalID == "" || executionPlan.Goal.Revision < 1 {
		return fmt.Errorf("%w: plan is not bound to a goal revision", plan.ErrPlanInvalid)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin save plan tx: %w", err)
	}
	defer tx.Rollback()

	var currentVersion int64
	err = tx.QueryRowContext(ctx, `
		SELECT max(version) FROM execution_plans WHERE plan_id = ?
	`, executionPlan.ID).Scan(&currentVersion)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		// max() over no rows yields NULL, which fails to scan into int64.
		// That is the "plan does not exist yet" case, not a fault.
		currentVersion = 0
	}

	if expectedVersion == 0 {
		if currentVersion != 0 {
			return fmt.Errorf("%w: plan %s already exists at version %d",
				plan.ErrPlanConflict, executionPlan.ID, currentVersion)
		}
		if executionPlan.Version < 1 {
			executionPlan.Version = 1
		}
	} else {
		if currentVersion == 0 {
			return fmt.Errorf("%w: plan %s has no stored version to update",
				plan.ErrPlanNotFound, executionPlan.ID)
		}
		if currentVersion != expectedVersion {
			return fmt.Errorf("%w: stored version is %d, caller expected %d",
				plan.ErrPlanConflict, currentVersion, expectedVersion)
		}
		if executionPlan.Version <= expectedVersion {
			executionPlan.Version = expectedVersion + 1
		}
	}

	body, err := json.Marshal(executionPlan)
	if err != nil {
		return fmt.Errorf("marshal plan: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO execution_plans (
			plan_id, version, project_id, goal_id, goal_revision, state, mode,
			constitution_version, graph_digest, supersedes, revision_reason,
			plan_json, created_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
	`,
		executionPlan.ID,
		executionPlan.Version,
		string(executionPlan.ProjectID),
		executionPlan.Goal.GoalID,
		executionPlan.Goal.Revision,
		string(executionPlan.State),
		string(executionPlan.Mode),
		executionPlan.ConstitutionVersion.String(),
		executionPlan.Graph.Digest,
		executionPlan.Supersedes,
		executionPlan.RevisionReason,
		string(body),
		utcNow(),
	); err != nil {
		return fmt.Errorf("insert plan: %w", err)
	}

	// The active pointer moves to the version just written. A project has one
	// current plan; earlier versions remain readable by version.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO execution_plan_active (project_id, plan_id, version, updated_at)
		VALUES (?,?,?,?)
		ON CONFLICT(project_id) DO UPDATE SET
			plan_id = excluded.plan_id,
			version = excluded.version,
			updated_at = excluded.updated_at
	`, string(executionPlan.ProjectID), executionPlan.ID, executionPlan.Version, utcNow()); err != nil {
		return fmt.Errorf("update active plan: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit save plan: %w", err)
	}
	return nil
}

// GetPlan reads one plan version.
func (s *Store) GetPlan(ctx context.Context, planID string, version int64) (plan.ExecutionPlan, error) {
	var body string
	err := s.db.QueryRowContext(ctx, `
		SELECT plan_json FROM execution_plans WHERE plan_id = ? AND version = ?
	`, planID, version).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return plan.ExecutionPlan{}, fmt.Errorf("%w: plan %s version %d",
			plan.ErrPlanNotFound, planID, version)
	}
	if err != nil {
		return plan.ExecutionPlan{}, fmt.Errorf("read plan: %w", err)
	}
	return decodePlan(body)
}

// GetActivePlan reads the current plan for a project.
func (s *Store) GetActivePlan(ctx context.Context, project projectid.ID) (plan.ExecutionPlan, error) {
	var body string
	err := s.db.QueryRowContext(ctx, `
		SELECT p.plan_json
		FROM execution_plan_active a
		JOIN execution_plans p
			ON p.plan_id = a.plan_id AND p.version = a.version
		WHERE a.project_id = ?
	`, string(project)).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return plan.ExecutionPlan{}, fmt.Errorf("%w: project %s has no plan",
			plan.ErrPlanNotFound, project)
	}
	if err != nil {
		return plan.ExecutionPlan{}, fmt.Errorf("read active plan: %w", err)
	}
	return decodePlan(body)
}

// ListPlanVersions returns every stored version of a plan, oldest first.
//
// The history is what makes provenance real: it shows what was approved, what
// replaced it and why, rather than only the current state.
func (s *Store) ListPlanVersions(ctx context.Context, planID string) ([]plan.ExecutionPlan, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT plan_json FROM execution_plans WHERE plan_id = ? ORDER BY version ASC
	`, planID)
	if err != nil {
		return nil, fmt.Errorf("list plan versions: %w", err)
	}
	defer rows.Close()

	var versions []plan.ExecutionPlan
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return nil, fmt.Errorf("scan plan version: %w", err)
		}
		decoded, err := decodePlan(body)
		if err != nil {
			return nil, err
		}
		versions = append(versions, decoded)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list plan versions: %w", err)
	}
	return versions, nil
}

// PlansForGoal returns the plans bound to a specific Goal revision.
//
// Staleness is decided by comparing a plan's binding against the live Goal, so
// this is a query rather than a field read.
func (s *Store) PlansForGoal(ctx context.Context, goalID string, revision int64) ([]plan.ExecutionPlan, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT plan_json FROM execution_plans
		WHERE goal_id = ? AND goal_revision = ?
		ORDER BY plan_id ASC, version ASC
	`, goalID, revision)
	if err != nil {
		return nil, fmt.Errorf("list plans for goal: %w", err)
	}
	defer rows.Close()

	var plans []plan.ExecutionPlan
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return nil, fmt.Errorf("scan plan: %w", err)
		}
		decoded, err := decodePlan(body)
		if err != nil {
			return nil, err
		}
		plans = append(plans, decoded)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list plans for goal: %w", err)
	}
	return plans, nil
}

func decodePlan(body string) (plan.ExecutionPlan, error) {
	var decoded plan.ExecutionPlan
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		return plan.ExecutionPlan{}, fmt.Errorf("decode plan: %w", err)
	}
	return decoded, nil
}
