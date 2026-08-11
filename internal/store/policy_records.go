package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Zen1th53/slaves/internal/model"
)

var (
	privateKeyPattern       = regexp.MustCompile(`(?i)-----BEGIN(?: RSA| EC| OPENSSH| DSA)? PRIVATE KEY-----`)
	secretAssignmentPattern = regexp.MustCompile(`(?i)(?:api[_-]?key|password|access[_-]?token|secret)[[:space:]]*[:=][[:space:]]*[^[:space:]]+`)
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

func (s *Store) ValidateApproval(ctx context.Context, use model.ApprovalUse) (model.Approval, error) {
	var approval model.Approval
	var operation, status, createdAt string
	var target, approvedBy, commitHash, expiresAt sql.NullString
	var conditionsJSON string
	err := s.db.QueryRowContext(ctx, `
		SELECT approval_id, project_id, operation, scope, target, requested_by,
		       approved_by, status, commit_hash, conditions_json, created_at,
		       expires_at, revision
		FROM approvals WHERE approval_id = ?
	`, use.ID).Scan(&approval.ID, &approval.ProjectID, &operation, &approval.Scope,
		&target, &approval.RequestedBy, &approvedBy, &status, &commitHash,
		&conditionsJSON, &createdAt, &expiresAt, &approval.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Approval{}, fmt.Errorf("%w: approval %s does not exist", model.ErrApprovalRequired, use.ID)
	}
	if err != nil {
		return model.Approval{}, fmt.Errorf("read approval: %w", err)
	}
	approval.Operation = model.Operation(operation)
	approval.Status = model.ApprovalStatus(status)
	approval.Target = target.String
	approval.ApprovedBy = approvedBy.String
	approval.Commit = commitHash.String
	approval.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return model.Approval{}, fmt.Errorf("parse approval creation: %w", err)
	}
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
	now := use.Now.UTC()
	if use.Now.IsZero() {
		now = time.Now().UTC()
	}
	if approval.Status != model.ApprovalApproved || approval.ExpiresAt == nil ||
		!now.Before(*approval.ExpiresAt) || approval.Operation != use.Operation ||
		approval.Scope != use.Scope || approval.Target != use.Target ||
		approval.Commit != use.Commit || approval.Revision != use.ExpectedRevision {
		return model.Approval{}, fmt.Errorf("%w: approval context is invalid", model.ErrApprovalRequired)
	}
	return approval, nil
}

func (s *Store) ConsumeApproval(ctx context.Context, approvalID string, expectedRevision int64) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE approvals SET status = 'consumed', revision = revision + 1
		WHERE approval_id = ? AND status = 'approved' AND revision = ?
	`, approvalID, expectedRevision)
	if err != nil {
		return fmt.Errorf("consume approval: %w", err)
	}
	return requireOne(result, "consume approval")
}

func (s *Store) CreateFinding(ctx context.Context, finding model.Finding) error {
	if finding.ID == "" || finding.ProjectID == "" ||
		(finding.OwnerRole != model.RoleQA && finding.OwnerRole != model.RoleAppSec) ||
		finding.Title == "" {
		return fmt.Errorf("%w: incomplete finding", model.ErrInvalid)
	}
	if finding.Status == "" {
		finding.Status = model.FindingOpen
	}
	if finding.CreatedAt.IsZero() {
		finding.CreatedAt = time.Now().UTC()
	}
	if finding.UpdatedAt.IsZero() {
		finding.UpdatedAt = finding.CreatedAt
	}
	evidence, err := json.Marshal(nonNilStrings(finding.Evidence))
	if err != nil {
		return fmt.Errorf("%w: encode finding evidence: %v", model.ErrInvalid, err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO findings(
			finding_id, project_id, owner_role, severity, status, task_id,
			title, evidence_json, revision, created_at, updated_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, finding.ID, finding.ProjectID, finding.OwnerRole, finding.Severity,
		finding.Status, finding.TaskID, finding.Title, string(evidence),
		finding.Revision, finding.CreatedAt.UTC().Format(time.RFC3339Nano),
		finding.UpdatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("create finding: %w", err)
	}
	return nil
}

func (s *Store) TransitionFinding(ctx context.Context, transition model.FindingTransition) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin finding transition: %w", err)
	}
	defer tx.Rollback()
	var owner, current string
	var revision int64
	if err := tx.QueryRowContext(ctx, `
		SELECT owner_role, status, revision FROM findings WHERE finding_id = ?
	`, transition.ID).Scan(&owner, &current, &revision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: finding %s", model.ErrNotFound, transition.ID)
		}
		return fmt.Errorf("read finding: %w", err)
	}
	if revision != transition.ExpectedRevision {
		return fmt.Errorf("%w: stale finding revision", model.ErrConflict)
	}
	if !findingTransitionAllowed(model.Role(owner), model.FindingStatus(current), transition.ActorRole, transition.Status) {
		return fmt.Errorf("%w: role %s cannot transition %s finding to %s",
			model.ErrPolicyDenied, transition.ActorRole, owner, transition.Status)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE findings SET status = ?, revision = revision + 1, updated_at = ?
		WHERE finding_id = ? AND revision = ?
	`, transition.Status, utcNow(), transition.ID, transition.ExpectedRevision)
	if err != nil {
		return fmt.Errorf("transition finding: %w", err)
	}
	if err := requireOne(result, "transition finding"); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit finding transition: %w", err)
	}
	return nil
}

func findingTransitionAllowed(owner model.Role, current model.FindingStatus, actor model.Role, next model.FindingStatus) bool {
	if next == model.FindingAcceptedRisk {
		return false
	}
	if actor == model.RoleDeveloper {
		return next == model.FindingFixing || next == model.FindingReadyForRetest
	}
	if actor != owner {
		return false
	}
	if next == model.FindingClosed {
		return current == model.FindingReadyForRetest
	}
	return next == model.FindingAssigned || next == model.FindingOpen
}

func (s *Store) Remember(ctx context.Context, record model.MemoryRecord) error {
	if record.ID == "" || record.ProjectID == "" || record.Type == "" ||
		record.Status == "" || record.Confidence == "" || strings.TrimSpace(record.Body) == "" {
		return fmt.Errorf("%w: incomplete memory record", model.ErrInvalid)
	}
	if containsSecretMaterial(record.Body) {
		return fmt.Errorf("%w: general memory rejects secret-like content", model.ErrSecretMaterial)
	}
	provenance, err := json.Marshal(record.Provenance)
	if err != nil {
		return fmt.Errorf("%w: encode memory provenance: %v", model.ErrInvalid, err)
	}
	var lastVerified any
	if record.LastVerifiedAt != nil {
		lastVerified = record.LastVerifiedAt.UTC().Format(time.RFC3339Nano)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO memory_records(
			memory_id, project_id, memory_type, status, confidence, body,
			provenance_json, last_verified_at, revision, created_at, updated_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, record.ID, record.ProjectID, record.Type, record.Status, record.Confidence,
		record.Body, string(provenance), lastVerified, record.Revision,
		record.CreatedAt.UTC().Format(time.RFC3339Nano),
		record.UpdatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("remember record: %w", err)
	}
	return nil
}

func containsSecretMaterial(body string) bool {
	return privateKeyPattern.MatchString(body) || secretAssignmentPattern.MatchString(body)
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
