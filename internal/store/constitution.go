package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// This file owns the persistence of Process 00 constitutional state. It lives
// in the store package for the same reason every other canonical entity does:
// callers reach durable state through typed store methods that enforce the
// invariants, never through a raw handle that could bypass them.

// SessionConstitution binds a session to the constitutional semantics it
// started under.
type SessionConstitution struct {
	SessionID       string
	ProjectID       string
	Version         string
	InvariantDigest string
	Mode            string
	BoundAt         time.Time
}

// ConstitutionalDecisionRow is a persisted constitutional verdict.
type ConstitutionalDecisionRow struct {
	DecisionID     string
	ProjectID      string
	SessionID      string
	Version        string
	Process        int
	Domain         string
	Action         string
	Actor          string
	Surface        string
	Mode           string
	Outcome        string
	Reason         string
	BindingDigest  string
	StateDigest    string
	AdvisoryStatus string
	FindingsJSON   string
	EvaluatedAt    time.Time
}

// ConstitutionalViolationRow is a persisted constitutional breach.
type ConstitutionalViolationRow struct {
	ViolationID string
	ProjectID   string
	SessionID   string
	DecisionID  string
	Class       string
	InvariantID string
	Response    string
	Actor       string
	Surface     string
	Version     string
	Detail      string
	DetectedAt  time.Time
}

// BindSessionConstitution writes a session's constitution binding.
//
// The binding is immutable. Binding a session again to the same version is
// accepted so that a restart is idempotent; binding it to a different version
// returns ErrConflict, because a session whose rules changed underneath it
// would have had its earlier decisions silently reinterpreted.
func (s *Store) BindSessionConstitution(ctx context.Context, binding SessionConstitution) error {
	if binding.SessionID == "" || binding.ProjectID == "" || binding.Version == "" {
		return fmt.Errorf("%w: session, project and constitution version are required", errInvalidConstitutionalInput)
	}
	if binding.BoundAt.IsZero() {
		binding.BoundAt = time.Now().UTC()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin constitution binding: %w", err)
	}
	defer tx.Rollback()

	var existing string
	err = tx.QueryRowContext(ctx,
		"SELECT constitution_version FROM session_constitutions WHERE session_id = ?",
		binding.SessionID).Scan(&existing)
	switch {
	case err == nil:
		if existing != binding.Version {
			return fmt.Errorf("%w: session %s is bound to constitution %s and cannot be rebound to %s",
				errConstitutionConflict, binding.SessionID, existing, binding.Version)
		}
		return nil
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("read session constitution: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO session_constitutions(
			session_id, project_id, constitution_version, invariant_digest, mode, bound_at)
		VALUES(?, ?, ?, ?, ?, ?)
	`, binding.SessionID, binding.ProjectID, binding.Version,
		binding.InvariantDigest, binding.Mode,
		binding.BoundAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("bind session constitution: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit constitution binding: %w", err)
	}
	return nil
}

// GetSessionConstitution reads a session's binding. The boolean reports
// whether a binding exists.
func (s *Store) GetSessionConstitution(ctx context.Context, sessionID string) (SessionConstitution, bool, error) {
	var (
		binding SessionConstitution
		boundAt string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT session_id, project_id, constitution_version, invariant_digest, mode, bound_at
		FROM session_constitutions WHERE session_id = ?
	`, sessionID).Scan(&binding.SessionID, &binding.ProjectID, &binding.Version,
		&binding.InvariantDigest, &binding.Mode, &boundAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return SessionConstitution{}, false, nil
	case err != nil:
		return SessionConstitution{}, false, fmt.Errorf("read session constitution: %w", err)
	}
	if parsed, parseErr := time.Parse(time.RFC3339Nano, boundAt); parseErr == nil {
		binding.BoundAt = parsed
	}
	return binding, true, nil
}

// RecordConstitutionalDecision persists a verdict together with every
// violation it evidenced, in one transaction.
//
// The two are written atomically because an audit trail showing a decision
// without the violations it produced would understate what happened, and one
// showing violations with no decision would be unattributable.
//
// The decision ID is stable for one decision, so a retry of the identical
// decision updates the existing row rather than forging a second history.
func (s *Store) RecordConstitutionalDecision(ctx context.Context, decision ConstitutionalDecisionRow, violations []ConstitutionalViolationRow) error {
	if decision.DecisionID == "" || decision.ProjectID == "" || decision.SessionID == "" {
		return fmt.Errorf("%w: decision, project and session are required", errInvalidConstitutionalInput)
	}
	if decision.EvaluatedAt.IsZero() {
		decision.EvaluatedAt = time.Now().UTC()
	}
	if decision.FindingsJSON == "" {
		decision.FindingsJSON = "[]"
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin constitutional record: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO constitutional_decisions(
			decision_id, project_id, session_id, constitution_version, process,
			domain, action, actor, surface, mode, outcome, reason,
			binding_digest, state_digest, advisory_status, findings_json, evaluated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(decision_id) DO UPDATE SET
			outcome = excluded.outcome,
			reason = excluded.reason,
			binding_digest = excluded.binding_digest,
			state_digest = excluded.state_digest,
			advisory_status = excluded.advisory_status,
			findings_json = excluded.findings_json,
			evaluated_at = excluded.evaluated_at
	`,
		decision.DecisionID, decision.ProjectID, decision.SessionID, decision.Version,
		decision.Process, decision.Domain, decision.Action, decision.Actor,
		decision.Surface, decision.Mode, decision.Outcome, decision.Reason,
		decision.BindingDigest, decision.StateDigest, decision.AdvisoryStatus,
		decision.FindingsJSON, decision.EvaluatedAt.UTC().Format(time.RFC3339Nano),
	); err != nil {
		return fmt.Errorf("record constitutional decision: %w", err)
	}

	for _, violation := range violations {
		if violation.DetectedAt.IsZero() {
			violation.DetectedAt = decision.EvaluatedAt
		}
		var decisionID any
		if violation.DecisionID != "" {
			decisionID = violation.DecisionID
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO constitutional_violations(
				violation_id, project_id, session_id, decision_id, violation_class,
				invariant_id, response, actor, surface, constitution_version,
				detail, detected_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(violation_id) DO NOTHING
		`,
			violation.ViolationID, violation.ProjectID, violation.SessionID, decisionID,
			violation.Class, violation.InvariantID, violation.Response, violation.Actor,
			violation.Surface, violation.Version, violation.Detail,
			violation.DetectedAt.UTC().Format(time.RFC3339Nano),
		); err != nil {
			return fmt.Errorf("record constitutional violation: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit constitutional record: %w", err)
	}
	return nil
}

// ListOpenConstitutionalViolations returns a session's unresolved violations.
func (s *Store) ListOpenConstitutionalViolations(ctx context.Context, sessionID string) ([]ConstitutionalViolationRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT violation_id, project_id, session_id, COALESCE(decision_id, ''),
		       violation_class, invariant_id, response, actor, surface,
		       constitution_version, detail, detected_at
		FROM constitutional_violations
		WHERE session_id = ? AND resolved_at IS NULL
		ORDER BY detected_at, violation_class
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("read constitutional violations: %w", err)
	}
	defer rows.Close()

	var violations []ConstitutionalViolationRow
	for rows.Next() {
		var (
			violation  ConstitutionalViolationRow
			detectedAt string
		)
		if err := rows.Scan(&violation.ViolationID, &violation.ProjectID, &violation.SessionID,
			&violation.DecisionID, &violation.Class, &violation.InvariantID, &violation.Response,
			&violation.Actor, &violation.Surface, &violation.Version, &violation.Detail,
			&detectedAt); err != nil {
			return nil, fmt.Errorf("scan constitutional violation: %w", err)
		}
		if parsed, parseErr := time.Parse(time.RFC3339Nano, detectedAt); parseErr == nil {
			violation.DetectedAt = parsed
		}
		violations = append(violations, violation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate constitutional violations: %w", err)
	}
	return violations, nil
}

// ListConstitutionalDecisions returns a session's recorded decisions, oldest
// first, bounded by limit.
func (s *Store) ListConstitutionalDecisions(ctx context.Context, sessionID string, limit int) ([]ConstitutionalDecisionRow, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT decision_id, project_id, session_id, constitution_version, process,
		       domain, action, actor, surface, mode, outcome, reason,
		       binding_digest, state_digest, advisory_status, findings_json, evaluated_at
		FROM constitutional_decisions
		WHERE session_id = ?
		ORDER BY evaluated_at, decision_id
		LIMIT ?
	`, sessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("read constitutional decisions: %w", err)
	}
	defer rows.Close()

	var decisions []ConstitutionalDecisionRow
	for rows.Next() {
		var (
			decision    ConstitutionalDecisionRow
			evaluatedAt string
		)
		if err := rows.Scan(&decision.DecisionID, &decision.ProjectID, &decision.SessionID,
			&decision.Version, &decision.Process, &decision.Domain, &decision.Action,
			&decision.Actor, &decision.Surface, &decision.Mode, &decision.Outcome,
			&decision.Reason, &decision.BindingDigest, &decision.StateDigest,
			&decision.AdvisoryStatus, &decision.FindingsJSON, &evaluatedAt); err != nil {
			return nil, fmt.Errorf("scan constitutional decision: %w", err)
		}
		if parsed, parseErr := time.Parse(time.RFC3339Nano, evaluatedAt); parseErr == nil {
			decision.EvaluatedAt = parsed
		}
		decisions = append(decisions, decision)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate constitutional decisions: %w", err)
	}
	return decisions, nil
}

// FindDecisionsByBinding returns decisions sharing an approval binding digest
// within a project. Approval replay is detectable through this index: the same
// binding appearing under a different actor or at a different time is exactly
// the pattern a replayed approval leaves behind.
func (s *Store) FindDecisionsByBinding(ctx context.Context, projectID, bindingDigest string) ([]ConstitutionalDecisionRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT decision_id, project_id, session_id, constitution_version, process,
		       domain, action, actor, surface, mode, outcome, reason,
		       binding_digest, state_digest, advisory_status, findings_json, evaluated_at
		FROM constitutional_decisions
		WHERE project_id = ? AND binding_digest = ?
		ORDER BY evaluated_at, decision_id
	`, projectID, bindingDigest)
	if err != nil {
		return nil, fmt.Errorf("read decisions by binding: %w", err)
	}
	defer rows.Close()

	var decisions []ConstitutionalDecisionRow
	for rows.Next() {
		var (
			decision    ConstitutionalDecisionRow
			evaluatedAt string
		)
		if err := rows.Scan(&decision.DecisionID, &decision.ProjectID, &decision.SessionID,
			&decision.Version, &decision.Process, &decision.Domain, &decision.Action,
			&decision.Actor, &decision.Surface, &decision.Mode, &decision.Outcome,
			&decision.Reason, &decision.BindingDigest, &decision.StateDigest,
			&decision.AdvisoryStatus, &decision.FindingsJSON, &evaluatedAt); err != nil {
			return nil, fmt.Errorf("scan decision: %w", err)
		}
		if parsed, parseErr := time.Parse(time.RFC3339Nano, evaluatedAt); parseErr == nil {
			decision.EvaluatedAt = parsed
		}
		decisions = append(decisions, decision)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate decisions: %w", err)
	}
	return decisions, nil
}

var (
	errInvalidConstitutionalInput = errors.New("constitutional record is incomplete")
	errConstitutionConflict       = errors.New("constitution binding conflict")
)

// ErrConstitutionConflict reports an attempt to rebind a session to a
// different constitution version.
func ErrConstitutionConflict() error { return errConstitutionConflict }
