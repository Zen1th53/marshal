package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/model"
)

// WriteMemoryV2 writes a canonical MemoryRecordV2 to memory_records_v2.
// The record is validated before write. The ContentDigest is computed and
// stored by this method — callers must not set it themselves.
func (s *Store) WriteMemoryV2(ctx context.Context, rec model.MemoryRecordV2) error {
	if err := rec.Validate(); err != nil {
		return fmt.Errorf("memory write rejected: %w", err)
	}

	fw := security.NewFirewall(security.FirewallConfig{})
	if err := fw.ScanRecord(ctx, rec); err != nil {
		return fmt.Errorf("memory write firewall rejected: %w", err)
	}

	// Compute and assign content digest.
	rec.ContentDigest = rec.CanonicalDigest()

	sourceJSON, err := json.Marshal(rec.Source)
	if err != nil {
		return fmt.Errorf("%w: encode memory source: %v", model.ErrInvalid, err)
	}

	evidenceIDs := strings.Join(rec.EvidenceIDs, ",")
	supersededBy := strings.Join(rec.SupersededBy, ",")
	supersedes := strings.Join(rec.SupersedesID, ",")
	conflictIDs := strings.Join(rec.ConflictIDs, ",")

	extMetaJSON := []byte("{}")
	if rec.ExtMeta != nil {
		if extMetaJSON, err = json.Marshal(rec.ExtMeta); err != nil {
			return fmt.Errorf("%w: encode ext_meta: %v", model.ErrInvalid, err)
		}
	}

	var validTo any
	if rec.ValidTo != nil {
		validTo = rec.ValidTo.UTC().Format(time.RFC3339Nano)
	}
	var lastVerifiedAt any
	if rec.LastVerifiedAt != nil {
		lastVerifiedAt = rec.LastVerifiedAt.UTC().Format(time.RFC3339Nano)
	}

	now := time.Now().UTC()
	if rec.IngestedAt.IsZero() {
		rec.IngestedAt = now
	}
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = now
	}
	if rec.UpdatedAt.IsZero() {
		rec.UpdatedAt = now
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin write memory tx: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO memory_records_v2(
			memory_id, project_id, kind, lifecycle, confidence, authority,
			title, body, content_digest, scope, scope_id,
			source_json, evidence_ids, head_commit, branch_name, worktree_id,
			session_id, run_id,
			observed_at, ingested_at, valid_from, valid_to, last_verified_at,
			revision, superseded_by, supersedes, conflict_ids, acl_scope,
			created_at, updated_at, ext_meta_json
		) VALUES(
			?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?,
			?, ?, ?, ?, ?,
			?, ?,
			?, ?, ?, ?, ?,
			?, ?, ?, ?, ?,
			?, ?, ?
		)`,
		rec.ID, rec.ProjectID, string(rec.Kind), string(rec.Lifecycle), string(rec.Confidence), string(rec.Authority),
		rec.Title, rec.Body, rec.ContentDigest, rec.Scope, rec.ScopeID,
		string(sourceJSON), evidenceIDs, rec.HeadCommit, rec.BranchName, rec.WorktreeID,
		rec.SessionID, rec.RunID,
		rec.ObservedAt.UTC().Format(time.RFC3339Nano),
		rec.IngestedAt.UTC().Format(time.RFC3339Nano),
		rec.ValidFrom.UTC().Format(time.RFC3339Nano),
		validTo, lastVerifiedAt,
		rec.Revision, supersededBy, supersedes, conflictIDs, rec.ACLScope,
		rec.CreatedAt.UTC().Format(time.RFC3339Nano),
		rec.UpdatedAt.UTC().Format(time.RFC3339Nano),
		string(extMetaJSON),
	)
	if err != nil {
		return fmt.Errorf("write memory v2: %w", err)
	}

	if err := insertMemoryOutboxTx(ctx, tx, rec.ProjectID, rec.ID, "memory.created", rec); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit write memory v2: %w", err)
	}
	return nil
}

// GetMemoryV2 retrieves a single MemoryRecordV2 by project ID and memory ID.
func (s *Store) GetMemoryV2(ctx context.Context, projectID, memoryID string) (model.MemoryRecordV2, error) {
	var (
		rec                                          model.MemoryRecordV2
		kind, lifecycle, confidence, authority       string
		sourceJSON, evidenceIDs, extMetaJSON         string
		supersededBy, supersedes, conflictIDs        string
		observedAt, ingestedAt, validFrom            string
		createdAt, updatedAt                         string
		validTo, lastVerifiedAt                      sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT
			memory_id, project_id, kind, lifecycle, confidence, authority,
			title, body, content_digest, scope, scope_id,
			source_json, evidence_ids, head_commit, branch_name, worktree_id,
			session_id, run_id,
			observed_at, ingested_at, valid_from, valid_to, last_verified_at,
			revision, superseded_by, supersedes, conflict_ids, acl_scope,
			created_at, updated_at, ext_meta_json
		FROM memory_records_v2
		WHERE project_id = ? AND memory_id = ?
	`, projectID, memoryID).Scan(
		&rec.ID, &rec.ProjectID, &kind, &lifecycle, &confidence, &authority,
		&rec.Title, &rec.Body, &rec.ContentDigest, &rec.Scope, &rec.ScopeID,
		&sourceJSON, &evidenceIDs, &rec.HeadCommit, &rec.BranchName, &rec.WorktreeID,
		&rec.SessionID, &rec.RunID,
		&observedAt, &ingestedAt, &validFrom, &validTo, &lastVerifiedAt,
		&rec.Revision, &supersededBy, &supersedes, &conflictIDs, &rec.ACLScope,
		&createdAt, &updatedAt, &extMetaJSON,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return model.MemoryRecordV2{}, fmt.Errorf("%w: memory record %s not found", model.ErrNotFound, memoryID)
	}
	if err != nil {
		return model.MemoryRecordV2{}, fmt.Errorf("get memory v2: %w", err)
	}

	rec.Kind = model.MemoryKind(kind)
	rec.Lifecycle = model.MemoryLifecycle(lifecycle)
	rec.Confidence = model.MemoryConfidence(confidence)
	rec.Authority = model.MemoryAuthority(authority)

	if err := json.Unmarshal([]byte(sourceJSON), &rec.Source); err != nil {
		return model.MemoryRecordV2{}, fmt.Errorf("decode source: %w", err)
	}
	if evidenceIDs != "" {
		rec.EvidenceIDs = strings.Split(evidenceIDs, ",")
	}
	if supersededBy != "" {
		rec.SupersededBy = strings.Split(supersededBy, ",")
	}
	if supersedes != "" {
		rec.SupersedesID = strings.Split(supersedes, ",")
	}
	if conflictIDs != "" {
		rec.ConflictIDs = strings.Split(conflictIDs, ",")
	}
	if extMetaJSON != "" && extMetaJSON != "{}" {
		if err := json.Unmarshal([]byte(extMetaJSON), &rec.ExtMeta); err != nil {
			return model.MemoryRecordV2{}, fmt.Errorf("decode ext_meta: %w", err)
		}
	}

	parseTS := func(s string) (time.Time, error) {
		return time.Parse(time.RFC3339Nano, s)
	}

	var parseErr error
	if rec.ObservedAt, parseErr = parseTS(observedAt); parseErr != nil {
		return model.MemoryRecordV2{}, fmt.Errorf("parse observed_at: %w", parseErr)
	}
	if rec.IngestedAt, parseErr = parseTS(ingestedAt); parseErr != nil {
		return model.MemoryRecordV2{}, fmt.Errorf("parse ingested_at: %w", parseErr)
	}
	if rec.ValidFrom, parseErr = parseTS(validFrom); parseErr != nil {
		return model.MemoryRecordV2{}, fmt.Errorf("parse valid_from: %w", parseErr)
	}
	if rec.CreatedAt, parseErr = parseTS(createdAt); parseErr != nil {
		return model.MemoryRecordV2{}, fmt.Errorf("parse created_at: %w", parseErr)
	}
	if rec.UpdatedAt, parseErr = parseTS(updatedAt); parseErr != nil {
		return model.MemoryRecordV2{}, fmt.Errorf("parse updated_at: %w", parseErr)
	}
	if validTo.Valid {
		t, err := parseTS(validTo.String)
		if err != nil {
			return model.MemoryRecordV2{}, fmt.Errorf("parse valid_to: %w", err)
		}
		rec.ValidTo = &t
	}
	if lastVerifiedAt.Valid {
		t, err := parseTS(lastVerifiedAt.String)
		if err != nil {
			return model.MemoryRecordV2{}, fmt.Errorf("parse last_verified_at: %w", err)
		}
		rec.LastVerifiedAt = &t
	}

	return rec, nil
}

// FindMemoryByDigest retrieves a memory record by its project ID and canonical content digest.
func (s *Store) FindMemoryByDigest(ctx context.Context, projectID, contentDigest string) (model.MemoryRecordV2, error) {
	var (
		rec                                          model.MemoryRecordV2
		kind, lifecycle, confidence, authority       string
		sourceJSON, evidenceIDs, extMetaJSON         string
		supersededBy, supersedes, conflictIDs        string
		observedAt, ingestedAt, validFrom            string
		createdAt, updatedAt                         string
		validTo, lastVerifiedAt                      sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT
			memory_id, project_id, kind, lifecycle, confidence, authority,
			title, body, content_digest, scope, scope_id,
			source_json, evidence_ids, head_commit, branch_name, worktree_id,
			session_id, run_id,
			observed_at, ingested_at, valid_from, valid_to, last_verified_at,
			revision, superseded_by, supersedes, conflict_ids, acl_scope,
			created_at, updated_at, ext_meta_json
		FROM memory_records_v2
		WHERE project_id = ? AND content_digest = ?
		ORDER BY created_at DESC
		LIMIT 1
	`, projectID, contentDigest).Scan(
		&rec.ID, &rec.ProjectID, &kind, &lifecycle, &confidence, &authority,
		&rec.Title, &rec.Body, &rec.ContentDigest, &rec.Scope, &rec.ScopeID,
		&sourceJSON, &evidenceIDs, &rec.HeadCommit, &rec.BranchName, &rec.WorktreeID,
		&rec.SessionID, &rec.RunID,
		&observedAt, &ingestedAt, &validFrom, &validTo, &lastVerifiedAt,
		&rec.Revision, &supersededBy, &supersedes, &conflictIDs, &rec.ACLScope,
		&createdAt, &updatedAt, &extMetaJSON,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return model.MemoryRecordV2{}, fmt.Errorf("%w: memory record with digest %s not found", model.ErrNotFound, contentDigest)
	}
	if err != nil {
		return model.MemoryRecordV2{}, fmt.Errorf("find memory by digest: %w", err)
	}

	rec.Kind = model.MemoryKind(kind)
	rec.Lifecycle = model.MemoryLifecycle(lifecycle)
	rec.Confidence = model.MemoryConfidence(confidence)
	rec.Authority = model.MemoryAuthority(authority)

	if err := json.Unmarshal([]byte(sourceJSON), &rec.Source); err != nil {
		return model.MemoryRecordV2{}, fmt.Errorf("decode source: %w", err)
	}
	if evidenceIDs != "" {
		rec.EvidenceIDs = strings.Split(evidenceIDs, ",")
	}
	if supersededBy != "" {
		rec.SupersededBy = strings.Split(supersededBy, ",")
	}
	if supersedes != "" {
		rec.SupersedesID = strings.Split(supersedes, ",")
	}
	if conflictIDs != "" {
		rec.ConflictIDs = strings.Split(conflictIDs, ",")
	}
	if extMetaJSON != "" && extMetaJSON != "{}" {
		if err := json.Unmarshal([]byte(extMetaJSON), &rec.ExtMeta); err != nil {
			return model.MemoryRecordV2{}, fmt.Errorf("decode ext_meta: %w", err)
		}
	}

	parseTS := func(s string) (time.Time, error) {
		return time.Parse(time.RFC3339Nano, s)
	}

	var parseErr error
	if rec.ObservedAt, parseErr = parseTS(observedAt); parseErr != nil {
		return model.MemoryRecordV2{}, fmt.Errorf("parse observed_at: %w", parseErr)
	}
	if rec.IngestedAt, parseErr = parseTS(ingestedAt); parseErr != nil {
		return model.MemoryRecordV2{}, fmt.Errorf("parse ingested_at: %w", parseErr)
	}
	if rec.ValidFrom, parseErr = parseTS(validFrom); parseErr != nil {
		return model.MemoryRecordV2{}, fmt.Errorf("parse valid_from: %w", parseErr)
	}
	if rec.CreatedAt, parseErr = parseTS(createdAt); parseErr != nil {
		return model.MemoryRecordV2{}, fmt.Errorf("parse created_at: %w", parseErr)
	}
	if rec.UpdatedAt, parseErr = parseTS(updatedAt); parseErr != nil {
		return model.MemoryRecordV2{}, fmt.Errorf("parse updated_at: %w", parseErr)
	}
	if validTo.Valid {
		t, err := parseTS(validTo.String)
		if err != nil {
			return model.MemoryRecordV2{}, fmt.Errorf("parse valid_to: %w", err)
		}
		rec.ValidTo = &t
	}
	if lastVerifiedAt.Valid {
		t, err := parseTS(lastVerifiedAt.String)
		if err != nil {
			return model.MemoryRecordV2{}, fmt.Errorf("parse last_verified_at: %w", err)
		}
		rec.LastVerifiedAt = &t
	}

	return rec, nil
}

// MemoryQueryFilter specifies filtering parameters for memory queries.
type MemoryQueryFilter struct {
	ProjectID string
	Kind      model.MemoryKind
	Lifecycle model.MemoryLifecycle
	Scope     model.MemoryScopeKind
	ScopeID   string
	ActorID   string // for ACL / operator_private scope filtering
	ValidAsOf time.Time
	KnownAt   time.Time
	Limit     int
}

// ListMemoryV2 queries memory_records_v2 applying strict project and scope boundaries.
func (s *Store) ListMemoryV2(ctx context.Context, filter MemoryQueryFilter) ([]model.MemoryRecordV2, error) {
	if filter.ProjectID == "" {
		return nil, fmt.Errorf("%w: project ID is required for memory query", model.ErrInvalid)
	}

	query := `
		SELECT
			memory_id, project_id, kind, lifecycle, confidence, authority,
			title, body, content_digest, scope, scope_id,
			source_json, evidence_ids, head_commit, branch_name, worktree_id,
			session_id, run_id,
			observed_at, ingested_at, valid_from, valid_to, last_verified_at,
			revision, superseded_by, supersedes, conflict_ids, acl_scope,
			created_at, updated_at, ext_meta_json
		FROM memory_records_v2
		WHERE project_id = ?
	`
	args := []any{filter.ProjectID}

	if filter.Kind != "" {
		query += " AND kind = ?"
		args = append(args, string(filter.Kind))
	}
	if filter.Lifecycle != "" {
		query += " AND lifecycle = ?"
		args = append(args, string(filter.Lifecycle))
	}
	if filter.Scope != "" {
		query += " AND scope = ?"
		args = append(args, string(filter.Scope))
	}
	if filter.ScopeID != "" {
		query += " AND scope_id = ?"
		args = append(args, filter.ScopeID)
	}
	if !filter.KnownAt.IsZero() {
		query += " AND ingested_at <= ?"
		args = append(args, filter.KnownAt.UTC().Format(time.RFC3339Nano))
	}
	if !filter.ValidAsOf.IsZero() {
		asOfStr := filter.ValidAsOf.UTC().Format(time.RFC3339Nano)
		query += " AND valid_from <= ? AND (valid_to IS NULL OR valid_to >= ?)"
		args = append(args, asOfStr, asOfStr)
	}

	query += " ORDER BY created_at DESC"
	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list memory v2: %w", err)
	}
	defer rows.Close()

	var records []model.MemoryRecordV2
	for rows.Next() {
		var (
			rec                                          model.MemoryRecordV2
			kind, lifecycle, confidence, authority       string
			sourceJSON, evidenceIDs, extMetaJSON         string
			supersededBy, supersedes, conflictIDs        string
			observedAt, ingestedAt, validFrom            string
			createdAt, updatedAt                         string
			validTo, lastVerifiedAt                      sql.NullString
		)
		err := rows.Scan(
			&rec.ID, &rec.ProjectID, &kind, &lifecycle, &confidence, &authority,
			&rec.Title, &rec.Body, &rec.ContentDigest, &rec.Scope, &rec.ScopeID,
			&sourceJSON, &evidenceIDs, &rec.HeadCommit, &rec.BranchName, &rec.WorktreeID,
			&rec.SessionID, &rec.RunID,
			&observedAt, &ingestedAt, &validFrom, &validTo, &lastVerifiedAt,
			&rec.Revision, &supersededBy, &supersedes, &conflictIDs, &rec.ACLScope,
			&createdAt, &updatedAt, &extMetaJSON,
		)
		if err != nil {
			return nil, fmt.Errorf("scan memory v2 row: %w", err)
		}

		// Enforce ACL and operator-private scope boundaries before disclosure
		scopeObj, err := model.NewMemoryScope(rec.Scope, rec.ScopeID)
		if err == nil {
			if !scopeObj.AllowsRead(filter.ProjectID, filter.ActorID) {
				continue // Skip unauthorized record
			}
		}

		rec.Kind = model.MemoryKind(kind)
		rec.Lifecycle = model.MemoryLifecycle(lifecycle)
		rec.Confidence = model.MemoryConfidence(confidence)
		rec.Authority = model.MemoryAuthority(authority)

		if err := json.Unmarshal([]byte(sourceJSON), &rec.Source); err != nil {
			return nil, fmt.Errorf("decode source: %w", err)
		}
		if evidenceIDs != "" {
			rec.EvidenceIDs = strings.Split(evidenceIDs, ",")
		}
		if supersededBy != "" {
			rec.SupersededBy = strings.Split(supersededBy, ",")
		}
		if supersedes != "" {
			rec.SupersedesID = strings.Split(supersedes, ",")
		}
		if conflictIDs != "" {
			rec.ConflictIDs = strings.Split(conflictIDs, ",")
		}
		if extMetaJSON != "" && extMetaJSON != "{}" {
			if err := json.Unmarshal([]byte(extMetaJSON), &rec.ExtMeta); err != nil {
				return nil, fmt.Errorf("decode ext_meta: %w", err)
			}
		}

		parseTS := func(s string) (time.Time, error) {
			return time.Parse(time.RFC3339Nano, s)
		}

		if rec.ObservedAt, err = parseTS(observedAt); err != nil {
			return nil, fmt.Errorf("parse observed_at: %w", err)
		}
		if rec.IngestedAt, err = parseTS(ingestedAt); err != nil {
			return nil, fmt.Errorf("parse ingested_at: %w", err)
		}
		if rec.ValidFrom, err = parseTS(validFrom); err != nil {
			return nil, fmt.Errorf("parse valid_from: %w", err)
		}
		if rec.CreatedAt, err = parseTS(createdAt); err != nil {
			return nil, fmt.Errorf("parse created_at: %w", err)
		}
		if rec.UpdatedAt, err = parseTS(updatedAt); err != nil {
			return nil, fmt.Errorf("parse updated_at: %w", err)
		}
		if validTo.Valid {
			t, err := parseTS(validTo.String)
			if err != nil {
				return nil, fmt.Errorf("parse valid_to: %w", err)
			}
			rec.ValidTo = &t
		}
		if lastVerifiedAt.Valid {
			t, err := parseTS(lastVerifiedAt.String)
			if err != nil {
				return nil, fmt.Errorf("parse last_verified_at: %w", err)
			}
			rec.LastVerifiedAt = &t
		}

		records = append(records, rec)
	}

	return records, nil
}

// UpdateMemory performs a compare-and-swap (CAS) update on a MemoryRecordV2.
// It verifies that expectedRevision matches the stored record's revision,
// invokes mutator to apply changes, validates the resulting record,
// computes the updated ContentDigest, increments revision, and commits atomically.
func (s *Store) UpdateMemory(ctx context.Context, projectID, memoryID string, expectedRevision int64, mutator func(*model.MemoryRecordV2) error) (model.MemoryRecordV2, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.MemoryRecordV2{}, fmt.Errorf("begin update memory: %w", err)
	}
	defer tx.Rollback()

	// 1. Fetch existing record with row-level lock/transaction
	var (
		rec                                          model.MemoryRecordV2
		kind, lifecycle, confidence, authority       string
		sourceJSON, evidenceIDs, extMetaJSON         string
		supersededBy, supersedes, conflictIDs        string
		observedAt, ingestedAt, validFrom            string
		createdAt, updatedAt                         string
		validTo, lastVerifiedAt                      sql.NullString
	)
	err = tx.QueryRowContext(ctx, `
		SELECT
			memory_id, project_id, kind, lifecycle, confidence, authority,
			title, body, content_digest, scope, scope_id,
			source_json, evidence_ids, head_commit, branch_name, worktree_id,
			session_id, run_id,
			observed_at, ingested_at, valid_from, valid_to, last_verified_at,
			revision, superseded_by, supersedes, conflict_ids, acl_scope,
			created_at, updated_at, ext_meta_json
		FROM memory_records_v2
		WHERE project_id = ? AND memory_id = ?
	`, projectID, memoryID).Scan(
		&rec.ID, &rec.ProjectID, &kind, &lifecycle, &confidence, &authority,
		&rec.Title, &rec.Body, &rec.ContentDigest, &rec.Scope, &rec.ScopeID,
		&sourceJSON, &evidenceIDs, &rec.HeadCommit, &rec.BranchName, &rec.WorktreeID,
		&rec.SessionID, &rec.RunID,
		&observedAt, &ingestedAt, &validFrom, &validTo, &lastVerifiedAt,
		&rec.Revision, &supersededBy, &supersedes, &conflictIDs, &rec.ACLScope,
		&createdAt, &updatedAt, &extMetaJSON,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return model.MemoryRecordV2{}, fmt.Errorf("%w: memory record %s not found", model.ErrNotFound, memoryID)
	}
	if err != nil {
		return model.MemoryRecordV2{}, fmt.Errorf("read memory for update: %w", err)
	}

	// 2. CAS revision check
	if rec.Revision != expectedRevision {
		return model.MemoryRecordV2{}, fmt.Errorf("%w: memory record %s revision mismatch (expected %d, found %d)",
			model.ErrConflict, memoryID, expectedRevision, rec.Revision)
	}

	rec.Kind = model.MemoryKind(kind)
	rec.Lifecycle = model.MemoryLifecycle(lifecycle)
	rec.Confidence = model.MemoryConfidence(confidence)
	rec.Authority = model.MemoryAuthority(authority)

	if err := json.Unmarshal([]byte(sourceJSON), &rec.Source); err != nil {
		return model.MemoryRecordV2{}, fmt.Errorf("decode source: %w", err)
	}
	if evidenceIDs != "" {
		rec.EvidenceIDs = strings.Split(evidenceIDs, ",")
	}
	if supersededBy != "" {
		rec.SupersededBy = strings.Split(supersededBy, ",")
	}
	if supersedes != "" {
		rec.SupersedesID = strings.Split(supersedes, ",")
	}
	if conflictIDs != "" {
		rec.ConflictIDs = strings.Split(conflictIDs, ",")
	}
	if extMetaJSON != "" && extMetaJSON != "{}" {
		if err := json.Unmarshal([]byte(extMetaJSON), &rec.ExtMeta); err != nil {
			return model.MemoryRecordV2{}, fmt.Errorf("decode ext_meta: %w", err)
		}
	}

	parseTS := func(s string) (time.Time, error) {
		return time.Parse(time.RFC3339Nano, s)
	}

	if rec.ObservedAt, err = parseTS(observedAt); err != nil {
		return model.MemoryRecordV2{}, fmt.Errorf("parse observed_at: %w", err)
	}
	if rec.IngestedAt, err = parseTS(ingestedAt); err != nil {
		return model.MemoryRecordV2{}, fmt.Errorf("parse ingested_at: %w", err)
	}
	if rec.ValidFrom, err = parseTS(validFrom); err != nil {
		return model.MemoryRecordV2{}, fmt.Errorf("parse valid_from: %w", err)
	}
	if rec.CreatedAt, err = parseTS(createdAt); err != nil {
		return model.MemoryRecordV2{}, fmt.Errorf("parse created_at: %w", err)
	}
	if validTo.Valid {
		t, err := parseTS(validTo.String)
		if err != nil {
			return model.MemoryRecordV2{}, fmt.Errorf("parse valid_to: %w", err)
		}
		rec.ValidTo = &t
	}
	if lastVerifiedAt.Valid {
		t, err := parseTS(lastVerifiedAt.String)
		if err != nil {
			return model.MemoryRecordV2{}, fmt.Errorf("parse last_verified_at: %w", err)
		}
		rec.LastVerifiedAt = &t
	}

	// 3. Apply mutator
	if err := mutator(&rec); err != nil {
		return model.MemoryRecordV2{}, fmt.Errorf("mutator error: %w", err)
	}

	// 4. Validate updated record
	if err := rec.Validate(); err != nil {
		return model.MemoryRecordV2{}, fmt.Errorf("updated record invalid: %w", err)
	}

	fw := security.NewFirewall(security.FirewallConfig{})
	if err := fw.ScanRecord(ctx, rec); err != nil {
		return model.MemoryRecordV2{}, fmt.Errorf("updated record firewall rejected: %w", err)
	}

	rec.Revision = expectedRevision + 1
	rec.UpdatedAt = time.Now().UTC()
	rec.ContentDigest = rec.CanonicalDigest()

	// 5. Write back updated record in transaction
	updatedSourceJSON, err := json.Marshal(rec.Source)
	if err != nil {
		return model.MemoryRecordV2{}, fmt.Errorf("%w: encode updated source: %v", model.ErrInvalid, err)
	}
	updatedExtMetaJSON := []byte("{}")
	if rec.ExtMeta != nil {
		if updatedExtMetaJSON, err = json.Marshal(rec.ExtMeta); err != nil {
			return model.MemoryRecordV2{}, fmt.Errorf("%w: encode updated ext_meta: %v", model.ErrInvalid, err)
		}
	}

	var updatedValidTo any
	if rec.ValidTo != nil {
		updatedValidTo = rec.ValidTo.UTC().Format(time.RFC3339Nano)
	}
	var updatedLastVerifiedAt any
	if rec.LastVerifiedAt != nil {
		updatedLastVerifiedAt = rec.LastVerifiedAt.UTC().Format(time.RFC3339Nano)
	}

	res, err := tx.ExecContext(ctx, `
		UPDATE memory_records_v2 SET
			kind = ?, lifecycle = ?, confidence = ?, authority = ?,
			title = ?, body = ?, content_digest = ?, scope = ?, scope_id = ?,
			source_json = ?, evidence_ids = ?, head_commit = ?, branch_name = ?, worktree_id = ?,
			session_id = ?, run_id = ?,
			observed_at = ?, ingested_at = ?, valid_from = ?, valid_to = ?, last_verified_at = ?,
			revision = ?, superseded_by = ?, supersedes = ?, conflict_ids = ?, acl_scope = ?,
			updated_at = ?, ext_meta_json = ?
		WHERE project_id = ? AND memory_id = ? AND revision = ?
	`,
		string(rec.Kind), string(rec.Lifecycle), string(rec.Confidence), string(rec.Authority),
		rec.Title, rec.Body, rec.ContentDigest, rec.Scope, rec.ScopeID,
		string(updatedSourceJSON), strings.Join(rec.EvidenceIDs, ","), rec.HeadCommit, rec.BranchName, rec.WorktreeID,
		rec.SessionID, rec.RunID,
		rec.ObservedAt.UTC().Format(time.RFC3339Nano),
		rec.IngestedAt.UTC().Format(time.RFC3339Nano),
		rec.ValidFrom.UTC().Format(time.RFC3339Nano),
		updatedValidTo, updatedLastVerifiedAt,
		rec.Revision, strings.Join(rec.SupersededBy, ","), strings.Join(rec.SupersedesID, ","),
		strings.Join(rec.ConflictIDs, ","), rec.ACLScope,
		rec.UpdatedAt.UTC().Format(time.RFC3339Nano), string(updatedExtMetaJSON),
		projectID, memoryID, expectedRevision,
	)
	if err != nil {
		return model.MemoryRecordV2{}, fmt.Errorf("update memory row: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	if rowsAffected == 0 {
		return model.MemoryRecordV2{}, fmt.Errorf("%w: concurrent modification on memory %s", model.ErrConflict, memoryID)
	}

	eventType := "memory.updated"
	if rec.Lifecycle == model.MemorySuperseded {
		eventType = "memory.superseded"
	} else if rec.Lifecycle == model.MemoryTombstoned {
		eventType = "memory.tombstoned"
	}

	if err := insertMemoryOutboxTx(ctx, tx, projectID, memoryID, eventType, rec); err != nil {
		return model.MemoryRecordV2{}, err
	}

	if err := tx.Commit(); err != nil {
		return model.MemoryRecordV2{}, fmt.Errorf("commit update memory: %w", err)
	}

	return rec, nil
}

// SupersedeMemory transitions an existing record to 'superseded', referencing newerID.
func (s *Store) SupersedeMemory(ctx context.Context, projectID, memoryID string, expectedRevision int64, newerID string) (model.MemoryRecordV2, error) {
	return s.UpdateMemory(ctx, projectID, memoryID, expectedRevision, func(m *model.MemoryRecordV2) error {
		m.Lifecycle = model.MemorySuperseded
		if newerID != "" {
			alreadyPresent := false
			for _, id := range m.SupersededBy {
				if id == newerID {
					alreadyPresent = true
					break
				}
			}
			if !alreadyPresent {
				m.SupersededBy = append(m.SupersededBy, newerID)
			}
		}
		return nil
	})
}

// TombstoneMemory marks a memory record as tombstoned with a rationale reason.
func (s *Store) TombstoneMemory(ctx context.Context, projectID, memoryID string, expectedRevision int64, reason string) (model.MemoryRecordV2, error) {
	return s.UpdateMemory(ctx, projectID, memoryID, expectedRevision, func(m *model.MemoryRecordV2) error {
		m.Lifecycle = model.MemoryTombstoned
		if m.ExtMeta == nil {
			m.ExtMeta = make(map[string]any)
		}
		m.ExtMeta["tombstone_reason"] = reason
		return nil
	})
}

