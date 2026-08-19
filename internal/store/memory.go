package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// WriteMemoryV2 writes a canonical MemoryRecordV2 to memory_records_v2.
// The record is validated before write. The ContentDigest is computed and
// stored by this method — callers must not set it themselves.
func (s *Store) WriteMemoryV2(ctx context.Context, rec model.MemoryRecordV2) error {
	if err := rec.Validate(); err != nil {
		return fmt.Errorf("memory write rejected: %w", err)
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

	_, err = s.db.ExecContext(ctx, `
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
