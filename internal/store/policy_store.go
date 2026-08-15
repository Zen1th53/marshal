package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policy"
)

// PolicyRecord is the durable, validated policy snapshot owned by the
// canonical store. The normalized payload is represented by Policy; the
// binding preserves the freshness identity required after restart.
type PolicyRecord struct {
	Policy      policy.Policy
	Binding     policy.PolicyBinding
	SourceRef   string
	Status      string
	ActivatedAt *time.Time
}

func (r PolicyRecord) validate() ([]byte, error) {
	if err := r.Policy.Validate(); err != nil {
		return nil, err
	}
	digest, err := r.Policy.Digest()
	if err != nil {
		return nil, err
	}
	if err := r.Binding.Validate(); err != nil || r.Binding.Version != r.Policy.Version || r.Binding.Digest != digest {
		return nil, fmt.Errorf("%w: policy binding does not match canonical payload", model.ErrInvalid)
	}
	if r.SourceRef != "" && len(r.SourceRef) > 1024 {
		return nil, fmt.Errorf("%w: policy source reference is too long", model.ErrInvalid)
	}
	status := r.Status
	if status == "" {
		status = "draft"
	}
	if status != "draft" && status != "active" && status != "retired" {
		return nil, fmt.Errorf("%w: unknown policy status", model.ErrInvalid)
	}
	if r.ActivatedAt != nil && r.ActivatedAt.IsZero() {
		return nil, fmt.Errorf("%w: invalid policy activation time", model.ErrInvalid)
	}
	return r.Policy.CanonicalJSON()
}

// PutPolicy durably stores one validated policy version atomically. Repeating
// the exact canonical record is idempotent; a different payload for the same
// policy identity is an immutable conflict.
func (s *Store) PutPolicy(ctx context.Context, record PolicyRecord) error {
	for attempt := 0; ; attempt++ {
		err := s.putPolicyOnce(ctx, record)
		if err == nil || !isSQLiteBusy(err) || attempt >= sqliteBusyRetries {
			return err
		}
		if err := waitSQLiteRetry(ctx, attempt); err != nil {
			return err
		}
	}
}

func (s *Store) putPolicyOnce(ctx context.Context, record PolicyRecord) error {
	canonical, err := record.validate()
	if err != nil {
		return err
	}
	status := record.Status
	if status == "" {
		status = "draft"
	}
	activatedAt := ""
	if record.ActivatedAt != nil {
		activatedAt = record.ActivatedAt.UTC().Format(time.RFC3339Nano)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin policy persistence: %w", err)
	}
	defer tx.Rollback()

	var storedDigest, storedPayload, storedSource, storedStatus, storedActivated string
	var storedGeneration int64
	err = tx.QueryRowContext(ctx, `
		SELECT digest, generation, source_ref, normalized_rules, status, COALESCE(activated_at, '')
		FROM policy_versions WHERE policy_id = ? AND version = ?
	`, string(record.Policy.ID), int64(record.Policy.Version)).Scan(&storedDigest, &storedGeneration, &storedSource, &storedPayload, &storedStatus, &storedActivated)
	if err == nil {
		if storedDigest == string(record.Binding.Digest) && storedGeneration == int64(record.Binding.Generation) &&
			storedSource == record.SourceRef && storedPayload == string(canonical) && storedStatus == status && storedActivated == activatedAt {
			return nil
		}
		return fmt.Errorf("%w: policy version is immutable", model.ErrConflict)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read policy version: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO policy_versions(policy_id, version, digest, generation, source_ref, normalized_rules, status, activated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''))
	`, string(record.Policy.ID), int64(record.Policy.Version), string(record.Binding.Digest), int64(record.Binding.Generation), record.SourceRef, string(canonical), status, activatedAt); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
			return fmt.Errorf("%w: policy version is immutable", model.ErrConflict)
		}
		return fmt.Errorf("insert policy version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit policy persistence: %w", err)
	}
	return nil
}

// PutPolicyVersion is a convenience form for callers that already hold the
// policy freshness binding.
func (s *Store) PutPolicyVersion(ctx context.Context, p policy.Policy, binding policy.PolicyBinding, sourceRef, status string, activatedAt *time.Time) error {
	return s.PutPolicy(ctx, PolicyRecord{Policy: p, Binding: binding, SourceRef: sourceRef, Status: status, ActivatedAt: activatedAt})
}

// GetPolicy loads and revalidates a policy snapshot from durable state.
func (s *Store) GetPolicy(ctx context.Context, id policy.PolicyID, version policy.PolicyVersion) (PolicyRecord, error) {
	var digest, payload, source, status, activated string
	var generation int64
	err := s.db.QueryRowContext(ctx, `
		SELECT digest, generation, source_ref, normalized_rules, status, COALESCE(activated_at, '')
		FROM policy_versions WHERE policy_id = ? AND version = ?
	`, string(id), int64(version)).Scan(&digest, &generation, &source, &payload, &status, &activated)
	if errors.Is(err, sql.ErrNoRows) {
		return PolicyRecord{}, fmt.Errorf("%w: policy version not found", model.ErrNotFound)
	}
	if err != nil {
		return PolicyRecord{}, fmt.Errorf("read policy version: %w", err)
	}
	p, err := policy.Parse([]byte(payload))
	if err != nil {
		return PolicyRecord{}, fmt.Errorf("%w: invalid durable policy", model.ErrInvalid)
	}
	canonical, canonicalErr := p.CanonicalJSON()
	computed, err := p.Digest()
	if canonicalErr != nil || err != nil || string(canonical) != payload || computed != policy.PolicyDigest(digest) || p.ID != id || p.Version != version || generation < 0 {
		return PolicyRecord{}, fmt.Errorf("%w: durable policy digest mismatch", model.ErrInvalid)
	}
	if status != "draft" && status != "active" && status != "retired" {
		return PolicyRecord{}, fmt.Errorf("%w: invalid durable policy status", model.ErrInvalid)
	}
	if len(source) > 1024 {
		return PolicyRecord{}, fmt.Errorf("%w: invalid durable policy source reference", model.ErrInvalid)
	}
	binding := policy.PolicyBinding{Version: version, Digest: computed, Generation: uint64(generation)}
	if err := binding.Validate(); err != nil {
		return PolicyRecord{}, fmt.Errorf("%w: invalid durable policy binding", model.ErrInvalid)
	}
	var activatedAt *time.Time
	if activated != "" {
		parsed, parseErr := time.Parse(time.RFC3339Nano, activated)
		if parseErr != nil {
			return PolicyRecord{}, fmt.Errorf("%w: invalid durable activation time", model.ErrInvalid)
		}
		parsed = parsed.UTC()
		activatedAt = &parsed
	}
	return PolicyRecord{Policy: p, Binding: binding, SourceRef: source, Status: status, ActivatedAt: activatedAt}, nil
}

// GetPolicyVersion is an explicit alias for versioned policy lookup.
func (s *Store) GetPolicyVersion(ctx context.Context, id policy.PolicyID, version policy.PolicyVersion) (PolicyRecord, error) {
	return s.GetPolicy(ctx, id, version)
}
