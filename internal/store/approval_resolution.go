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

// ListPendingApprovals returns approvals still awaiting an operator decision.
// Pending means status 'requested': it has neither been approved, denied, expired,
// consumed nor revoked. Results are ordered oldest first so the operator resolves
// the longest-waiting request first.
func (s *Store) ListPendingApprovals(ctx context.Context, projectID string) ([]model.Approval, error) {
	query := `
		SELECT approval_id, project_id, operation, scope, target, requested_by,
		       approved_by, status, commit_hash, conditions_json, created_at,
		       expires_at, revision
		FROM approvals WHERE status = 'requested'`
	args := []any{}
	if projectID != "" {
		query += ` AND project_id = ?`
		args = append(args, projectID)
	}
	query += ` ORDER BY created_at ASC, approval_id ASC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list pending approvals: %w", err)
	}
	defer rows.Close()

	var approvals []model.Approval
	for rows.Next() {
		approval, err := scanApproval(rows)
		if err != nil {
			return nil, err
		}
		approvals = append(approvals, approval)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending approvals: %w", err)
	}
	return approvals, nil
}

// GetApproval reads a single approval record by identifier.
func (s *Store) GetApproval(ctx context.Context, approvalID string) (model.Approval, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT approval_id, project_id, operation, scope, target, requested_by,
		       approved_by, status, commit_hash, conditions_json, created_at,
		       expires_at, revision
		FROM approvals WHERE approval_id = ?
	`, approvalID)
	approval, err := scanApproval(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Approval{}, fmt.Errorf("%w: approval %s", model.ErrNotFound, approvalID)
	}
	if err != nil {
		return model.Approval{}, err
	}
	return approval, nil
}

// ResolveApproval durably records an operator decision on a pending approval.
//
// Only the 'requested' state may be resolved, and the caller must supply the
// revision it observed, so two concurrent operators cannot both resolve the same
// request: the loser fails with model.ErrConflict. An approval carries a
// mandatory expiry, matching the CreateApproval invariant, so an approved record
// can never be open ended. A denial is retained rather than deleted, which keeps
// the rejection auditable.
func (s *Store) ResolveApproval(ctx context.Context, approvalID, decidedBy string, approve bool, expiresAt *time.Time, expectedRevision int64) (model.Approval, error) {
	if approvalID == "" || decidedBy == "" {
		return model.Approval{}, fmt.Errorf("%w: approval id and decider are required", model.ErrInvalid)
	}
	if approve && expiresAt == nil {
		return model.Approval{}, fmt.Errorf("%w: approved record requires expiry", model.ErrInvalid)
	}

	status := model.ApprovalDenied
	var expiry any
	if approve {
		status = model.ApprovalApproved
		expiry = expiresAt.UTC().Format(time.RFC3339Nano)
	}

	result, err := s.db.ExecContext(ctx, `
		UPDATE approvals
		SET status = ?, approved_by = ?, expires_at = ?, revision = revision + 1
		WHERE approval_id = ? AND status = 'requested' AND revision = ?
	`, status, decidedBy, expiry, approvalID, expectedRevision)
	if err != nil {
		return model.Approval{}, fmt.Errorf("resolve approval: %w", err)
	}
	if err := requireOne(result, "resolve approval"); err != nil {
		return model.Approval{}, err
	}
	return s.GetApproval(ctx, approvalID)
}

// scanApproval decodes one approvals row. It takes the package's rowScanner so
// both *sql.Row and *sql.Rows decode through a single path.
func scanApproval(scanner rowScanner) (model.Approval, error) {
	var approval model.Approval
	var operation, status, createdAt, conditionsJSON string
	var target, approvedBy, commitHash, expiresAt sql.NullString

	if err := scanner.Scan(&approval.ID, &approval.ProjectID, &operation, &approval.Scope,
		&target, &approval.RequestedBy, &approvedBy, &status, &commitHash,
		&conditionsJSON, &createdAt, &expiresAt, &approval.Revision); err != nil {
		return model.Approval{}, err
	}

	approval.Operation = model.Operation(operation)
	approval.Status = model.ApprovalStatus(status)
	approval.Target = target.String
	approval.ApprovedBy = approvedBy.String
	approval.Commit = commitHash.String

	parsed, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return model.Approval{}, fmt.Errorf("parse approval creation: %w", err)
	}
	approval.CreatedAt = parsed

	if expiresAt.Valid {
		value, err := time.Parse(time.RFC3339Nano, expiresAt.String)
		if err != nil {
			return model.Approval{}, fmt.Errorf("parse approval expiry: %w", err)
		}
		approval.ExpiresAt = &value
	}
	if err := json.Unmarshal([]byte(conditionsJSON), &approval.Conditions); err != nil {
		return model.Approval{}, fmt.Errorf("decode approval conditions: %w", err)
	}
	return approval, nil
}
