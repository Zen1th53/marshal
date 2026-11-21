package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func (s *Store) CreateApproval(ctx context.Context, approval model.Approval) error {
	if approval.ID == "" || approval.ProjectID == "" || approval.Operation == "" ||
		approval.Scope == "" || approval.RequestedBy == "" || approval.Status == "" {
		return fmt.Errorf("%w: incomplete approval", model.ErrInvalid)
	}
	if approval.Status == model.ApprovalApproved && approval.ExpiresAt == nil {
		return fmt.Errorf("%w: approved record requires expiry", model.ErrInvalid)
	}
	if approval.CreatedAt.IsZero() {
		approval.CreatedAt = time.Now().UTC()
	}
	conditions, err := json.Marshal(nonNilStrings(approval.Conditions))
	if err != nil {
		return fmt.Errorf("%w: encode approval conditions: %v", model.ErrInvalid, err)
	}
	var expiresAt any
	if approval.ExpiresAt != nil {
		expiresAt = approval.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO approvals(
			approval_id, project_id, operation, scope, target, requested_by,
			approved_by, status, commit_hash, conditions_json, created_at,
			expires_at, revision
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, approval.ID, approval.ProjectID, approval.Operation, approval.Scope,
		nullIfEmpty(approval.Target), approval.RequestedBy, nullIfEmpty(approval.ApprovedBy),
		approval.Status, nullIfEmpty(approval.Commit), string(conditions),
		approval.CreatedAt.UTC().Format(time.RFC3339Nano), expiresAt, approval.Revision)
	if err != nil {
		return fmt.Errorf("create approval: %w", err)
	}
	return nil
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
