package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/evidence"
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
	State       policy.State
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
	// SQLite stores generation as a signed INTEGER. Reject values that cannot
	// be represented before beginning a write instead of surfacing a backend
	// constraint error or relying on a lossy uint64-to-int64 conversion.
	if r.Binding.Generation > math.MaxInt64 {
		return nil, fmt.Errorf("%w: policy generation is out of durable range", model.ErrInvalid)
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
	if r.State != "" && !r.State.Valid() {
		return nil, fmt.Errorf("%w: invalid policy lifecycle state", model.ErrInvalid)
	}
	return r.Policy.CanonicalJSON()
}

// PutPolicy durably stores one validated policy version atomically. Repeating
// the exact canonical record is idempotent; a different payload for the same
// policy identity is an immutable conflict.
func (s *Store) PutPolicy(ctx context.Context, record PolicyRecord) error {
	started := time.Now()
	var result error
	defer func() { s.observePolicyMetric(evidence.MetricOperationPolicyPersist, result, started) }()
	for attempt := 0; ; attempt++ {
		err := s.putPolicyOnce(ctx, record)
		result = err
		if err == nil || !isSQLiteBusy(err) || attempt >= sqliteBusyRetries {
			return err
		}
		if waitErr := waitSQLiteRetry(ctx, attempt); waitErr != nil {
			result = waitErr
			return waitErr
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
	state := record.State
	if state == "" {
		state = policy.StateLoaded
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin policy persistence: %w", err)
	}
	defer tx.Rollback()

	var storedDigest, storedPayload, storedSource, storedStatus, storedState, storedActivated string
	var storedGeneration int64
	err = tx.QueryRowContext(ctx, `
		SELECT digest, generation, source_ref, normalized_rules, status, state, COALESCE(activated_at, '')
		FROM policy_versions WHERE policy_id = ? AND version = ?
	`, string(record.Policy.ID), int64(record.Policy.Version)).Scan(&storedDigest, &storedGeneration, &storedSource, &storedPayload, &storedStatus, &storedState, &storedActivated)
	if err == nil {
		if storedDigest == string(record.Binding.Digest) && storedGeneration == int64(record.Binding.Generation) &&
			storedSource == record.SourceRef && storedPayload == string(canonical) && storedStatus == status && storedState == string(state) && storedActivated == activatedAt {
			return nil
		}
		return fmt.Errorf("%w: policy version is immutable", model.ErrConflict)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read policy version: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO policy_versions(policy_id, version, digest, generation, source_ref, normalized_rules, status, state, activated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''))
	`, string(record.Policy.ID), int64(record.Policy.Version), string(record.Binding.Digest), int64(record.Binding.Generation), record.SourceRef, string(canonical), status, string(state), activatedAt); err != nil {
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
func (s *Store) GetPolicy(ctx context.Context, id policy.PolicyID, version policy.PolicyVersion) (record PolicyRecord, resultErr error) {
	started := time.Now()
	defer func() { s.observePolicyMetric(evidence.MetricOperationPolicyLoad, resultErr, started) }()
	var digest, payload, source, status, stateText, activated string
	var generation int64
	err := s.db.QueryRowContext(ctx, `
		SELECT digest, generation, source_ref, normalized_rules, status, state, COALESCE(activated_at, '')
		FROM policy_versions WHERE policy_id = ? AND version = ?
	`, string(id), int64(version)).Scan(&digest, &generation, &source, &payload, &status, &stateText, &activated)
	if errors.Is(err, sql.ErrNoRows) {
		resultErr = fmt.Errorf("%w: policy version not found", model.ErrNotFound)
		return PolicyRecord{}, resultErr
	}
	if err != nil {
		resultErr = fmt.Errorf("read policy version: %w", err)
		return PolicyRecord{}, resultErr
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
	state := policy.State(stateText)
	if !state.Valid() {
		return PolicyRecord{}, fmt.Errorf("%w: invalid durable policy lifecycle state", model.ErrInvalid)
	}
	binding := policy.PolicyBinding{Version: version, Digest: computed, Generation: uint64(generation)}
	if err := binding.Validate(); err != nil {
		return PolicyRecord{}, fmt.Errorf("%w: invalid durable policy binding", model.ErrInvalid)
	}
	var activatedAt *time.Time
	if activated != "" {
		parsed, parseErr := time.Parse(time.RFC3339Nano, activated)
		if parseErr != nil {
			resultErr = fmt.Errorf("%w: invalid durable activation time", model.ErrInvalid)
			return PolicyRecord{}, resultErr
		}
		parsed = parsed.UTC()
		activatedAt = &parsed
	}
	return PolicyRecord{Policy: p, Binding: binding, SourceRef: source, Status: status, State: state, ActivatedAt: activatedAt}, nil
}

// GetPolicyVersion is an explicit alias for versioned policy lookup.
func (s *Store) GetPolicyVersion(ctx context.Context, id policy.PolicyID, version policy.PolicyVersion) (PolicyRecord, error) {
	return s.GetPolicy(ctx, id, version)
}

// GetActivePolicy selects the sole lifecycle-authoritative policy snapshot.
// Ambiguous or missing active state is fail-closed for runtime callers.
func (s *Store) GetActivePolicy(ctx context.Context) (record PolicyRecord, resultErr error) {
	started := time.Now()
	defer func() { s.observePolicyMetric(evidence.MetricOperationPolicyLoad, resultErr, started) }()
	var id string
	var version int64
	err := s.db.QueryRowContext(ctx, `SELECT policy_id, version FROM policy_versions WHERE state = ? ORDER BY policy_id, version LIMIT 2`, string(policy.StateActive)).Scan(&id, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return PolicyRecord{}, fmt.Errorf("%w: active policy not found", model.ErrNotFound)
	}
	if err != nil {
		return PolicyRecord{}, fmt.Errorf("read active policy: %w", err)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM policy_versions WHERE state = ?`, string(policy.StateActive)).Scan(&count); err != nil {
		return PolicyRecord{}, fmt.Errorf("count active policies: %w", err)
	}
	if count != 1 {
		return PolicyRecord{}, fmt.Errorf("%w: active policy selection is ambiguous", model.ErrConflict)
	}
	return s.GetPolicy(ctx, policy.PolicyID(id), policy.PolicyVersion(version))
}
