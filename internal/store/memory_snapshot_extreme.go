//go:build !governed

package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/model"
)

const maxMemorySnapshotRecords = 128
const maxMemorySnapshotBytes = 4 << 20

type MemorySnapshotBinding struct {
	ProjectID        string `json:"project_id"`
	StorageProjectID string `json:"storage_project_id,omitempty"`
	SessionID        string `json:"session_id"`
	Actor            string `json:"actor"`
	HeadCommit       string `json:"head_commit"`
}

// MemorySnapshot durably captures the exact selected records, not an implicit
// full-project image. Records are full canonical bodies and provenance.
type MemorySnapshot struct {
	ID          string                 `json:"id"`
	Version     int64                  `json:"version"`
	Binding     MemorySnapshotBinding  `json:"binding"`
	Digest      string                 `json:"digest"`
	RecordCount int                    `json:"record_count"`
	MemoryIDs   []string               `json:"memory_ids"`
	CreatedAt   time.Time              `json:"created_at"`
	Records     []model.MemoryRecordV2 `json:"records,omitempty"`
}

type MemoryRestoreChange struct {
	MemoryID         string `json:"memory_id"`
	CurrentRevision  int64  `json:"current_revision"`
	SnapshotRevision int64  `json:"snapshot_revision"`
	BeforeDigest     string `json:"before_digest"`
	AfterDigest      string `json:"after_digest"`
	Title            string `json:"title"`
	BeforeBody       string `json:"before_body"`
	AfterBody        string `json:"after_body"`
	Truncated        bool   `json:"truncated"`
}

// MemorySnapshotPreview is an immutable durable confirmation receipt. Its
// complete bytes and current full-record state are rechecked on restore.
type MemorySnapshotPreview struct {
	ID              string                `json:"id"`
	Binding         MemorySnapshotBinding `json:"binding"`
	SnapshotID      string                `json:"snapshot_id"`
	SnapshotVersion int64                 `json:"snapshot_version"`
	SnapshotDigest  string                `json:"snapshot_digest"`
	CurrentDigest   string                `json:"current_digest"`
	Changes         []MemoryRestoreChange `json:"changes"`
	CreatedAt       time.Time             `json:"created_at"`
}

type MemorySnapshotRestore struct {
	PreviewID  string           `json:"preview_id"`
	SnapshotID string           `json:"snapshot_id"`
	Revisions  map[string]int64 `json:"revisions"`
}

type MemoryRestoreHistory struct {
	MemoryID string               `json:"memory_id"`
	Before   model.MemoryRecordV2 `json:"before"`
	After    model.MemoryRecordV2 `json:"after"`
}

func memorySnapshotIdentifier(id string) bool {
	if len(id) == 0 || len(id) > 256 {
		return false
	}
	for _, r := range id {
		if r < 33 || r > 126 {
			return false
		}
	}
	return true
}
func memorySnapshotProject(b MemorySnapshotBinding) string {
	if b.StorageProjectID != "" {
		return b.StorageProjectID
	}
	return b.ProjectID
}

func validMemorySnapshotBinding(b MemorySnapshotBinding) bool {
	if !memorySnapshotIdentifier(memorySnapshotProject(b)) || !memorySnapshotIdentifier(b.ProjectID) || !memorySnapshotIdentifier(b.SessionID) || !memorySnapshotIdentifier(b.Actor) {
		return false
	}
	if len(b.HeadCommit) != 40 && len(b.HeadCommit) != 64 {
		return false
	}
	for _, r := range b.HeadCommit {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
func memorySnapshotDigest(value any) (string, []byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", nil, err
	}
	if len(raw) > maxMemorySnapshotBytes {
		return "", nil, fmt.Errorf("%w: memory snapshot byte bound", model.ErrInvalid)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), raw, nil
}
func checkMemorySnapshotRecord(ctx context.Context, rec model.MemoryRecordV2, b MemorySnapshotBinding) error {
	if rec.ProjectID != memorySnapshotProject(b) || rec.ACLScope != "" && rec.ACLScope != b.Actor || rec.Scope == "project" && rec.ScopeID != memorySnapshotProject(b) || rec.Scope == "session" && rec.ScopeID != b.SessionID || (rec.Scope == "operator_private" || rec.Scope == "agent") && rec.ScopeID != b.Actor {
		return authz.ErrDenied
	}
	if err := rec.Validate(); err != nil {
		return err
	}
	if rec.Revision < 0 || rec.ContentDigest != rec.CanonicalDigest() {
		return fmt.Errorf("%w: invalid memory snapshot source revision or digest", model.ErrConflict)
	}
	if err := security.NewFirewall(security.FirewallConfig{}).ScanRecord(ctx, rec); err != nil {
		return err
	}
	_, raw, err := memorySnapshotDigest(rec)
	if err != nil {
		return err
	}
	if len(raw) > 1<<20 {
		return fmt.Errorf("%w: memory snapshot record byte bound", model.ErrInvalid)
	}
	return nil
}
func snapshotSelectedRecords(ctx context.Context, tx *sql.Tx, b MemorySnapshotBinding, ids []string) ([]model.MemoryRecordV2, error) {
	if !validMemorySnapshotBinding(b) || len(ids) == 0 || len(ids) > maxMemorySnapshotRecords {
		return nil, model.ErrInvalid
	}
	selected := append([]string(nil), ids...)
	sort.Strings(selected)
	var records []model.MemoryRecordV2
	totalBytes := 0
	for i, id := range selected {
		if !memorySnapshotIdentifier(id) || i > 0 && selected[i-1] == id {
			return nil, model.ErrInvalid
		}
		var sourceBytes int
		if err := tx.QueryRowContext(ctx, `SELECT length(CAST(body AS BLOB))+length(CAST(title AS BLOB))+length(CAST(ext_meta_json AS BLOB))+length(CAST(source_json AS BLOB))+length(CAST(evidence_ids AS BLOB))+length(CAST(supersedes AS BLOB))+length(CAST(superseded_by AS BLOB))+length(CAST(conflict_ids AS BLOB)) FROM memory_records_v2 WHERE project_id=? AND memory_id=? AND (acl_scope='' OR acl_scope=?)`, memorySnapshotProject(b), id, b.Actor).Scan(&sourceBytes); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, authz.ErrDenied
			}
			return nil, err
		}
		if sourceBytes > 1<<20 {
			return nil, fmt.Errorf("%w: memory snapshot source byte bound", model.ErrInvalid)
		}
		rec, err := getMemoryV2(ctx, tx, memorySnapshotProject(b), id)
		if err != nil {
			return nil, err
		}
		if err := checkMemorySnapshotRecord(ctx, rec, b); err != nil {
			return nil, err
		}
		_, raw, err := memorySnapshotDigest(rec)
		if err != nil {
			return nil, err
		}
		totalBytes += len(raw) + 1
		if totalBytes > maxMemorySnapshotBytes {
			return nil, model.ErrInvalid
		}
		records = append(records, rec)
	}
	_, _, err := memorySnapshotDigest(records)
	return records, err
}

func (s *Store) CreateMemorySnapshot(ctx context.Context, b MemorySnapshotBinding, id string, ids []string) (MemorySnapshot, error) {
	if !memorySnapshotIdentifier(id) {
		return MemorySnapshot{}, model.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MemorySnapshot{}, err
	}
	defer tx.Rollback()
	records, err := snapshotSelectedRecords(ctx, tx, b, ids)
	if err != nil {
		return MemorySnapshot{}, err
	}
	snapshot := MemorySnapshot{ID: id, Version: 1, Binding: b, RecordCount: len(records), CreatedAt: time.Now().UTC(), Records: records}
	for _, rec := range records {
		snapshot.MemoryIDs = append(snapshot.MemoryIDs, rec.ID)
	}
	snapshot.Digest, _, err = memorySnapshotDigest(snapshot)
	if err != nil {
		return MemorySnapshot{}, err
	}
	_, raw, err := memorySnapshotDigest(snapshot)
	if err != nil {
		return MemorySnapshot{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO extreme_memory_snapshots(project_id,snapshot_id,version,session_id,actor,digest,snapshot_json,created_at) VALUES(?,?,?,?,?,?,?,?)`, b.ProjectID, id, 1, b.SessionID, b.Actor, snapshot.Digest, string(raw), utcNow()); err != nil {
		return MemorySnapshot{}, err
	}
	if err := memorySnapshotCommandReceipt(ctx, tx, b, id, 1); err != nil {
		return MemorySnapshot{}, err
	}
	if err := tx.Commit(); err != nil {
		return MemorySnapshot{}, err
	}
	return snapshot, nil
}

// ListMemorySnapshots reads only bounded metadata for the exact authenticated
// binding. Archived full record bodies are never loaded into list results.
func (s *Store) ListMemorySnapshots(ctx context.Context, b MemorySnapshotBinding, limit int) ([]MemorySnapshot, error) {
	if !validMemorySnapshotBinding(b) || limit < 1 || limit > 50 {
		return nil, model.ErrInvalid
	}
	rows, err := s.db.QueryContext(ctx, `SELECT json_remove(snapshot_json,'$.records') FROM extreme_memory_snapshots WHERE project_id=? AND session_id=? AND actor=? AND COALESCE(json_extract(snapshot_json,'$.binding.storage_project_id'),'')=? ORDER BY created_at DESC,snapshot_id DESC LIMIT ?`, b.ProjectID, b.SessionID, b.Actor, b.StorageProjectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := make([]MemorySnapshot, 0)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if len(raw) > 40<<10 {
			return nil, model.ErrInvalid
		}
		var snapshot MemorySnapshot
		if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
			return nil, err
		}
		if !validMemorySnapshotBinding(snapshot.Binding) || snapshot.Binding.ProjectID != b.ProjectID || snapshot.Binding.StorageProjectID != b.StorageProjectID || snapshot.Binding.SessionID != b.SessionID || snapshot.Binding.Actor != b.Actor || !memorySnapshotIdentifier(snapshot.ID) || snapshot.Version != 1 || snapshot.RecordCount < 1 || snapshot.RecordCount > maxMemorySnapshotRecords || len(snapshot.MemoryIDs) != snapshot.RecordCount || len(snapshot.Records) != 0 {
			return nil, model.ErrConflict
		}
		if _, err := hex.DecodeString(snapshot.Digest); err != nil || len(snapshot.Digest) != 64 {
			return nil, model.ErrConflict
		}
		for i, id := range snapshot.MemoryIDs {
			if !memorySnapshotIdentifier(id) || i > 0 && snapshot.MemoryIDs[i-1] >= id {
				return nil, model.ErrConflict
			}
		}
		results = append(results, snapshot)
	}
	return results, rows.Err()
}

func loadMemorySnapshot(ctx context.Context, reader memoryRowReader, b MemorySnapshotBinding, id string, version int64) (MemorySnapshot, error) {
	if !validMemorySnapshotBinding(b) || !memorySnapshotIdentifier(id) || version != 1 {
		return MemorySnapshot{}, model.ErrInvalid
	}
	var raw, digest string
	err := reader.QueryRowContext(ctx, `SELECT snapshot_json,digest FROM extreme_memory_snapshots WHERE project_id=? AND snapshot_id=? AND version=? AND session_id=? AND actor=?`, b.ProjectID, id, version, b.SessionID, b.Actor).Scan(&raw, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return MemorySnapshot{}, model.ErrNotFound
	}
	if err != nil {
		return MemorySnapshot{}, err
	}
	if len(raw) > maxMemorySnapshotBytes {
		return MemorySnapshot{}, model.ErrInvalid
	}
	var snapshot MemorySnapshot
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		return MemorySnapshot{}, err
	}
	if snapshot.ID != id || snapshot.Version != version || snapshot.Binding.ProjectID != b.ProjectID || snapshot.Binding.StorageProjectID != b.StorageProjectID || snapshot.Binding.SessionID != b.SessionID || snapshot.Binding.Actor != b.Actor || snapshot.Digest != digest || snapshot.RecordCount != len(snapshot.Records) || len(snapshot.Records) == 0 || len(snapshot.Records) > maxMemorySnapshotRecords || len(snapshot.MemoryIDs) != len(snapshot.Records) {
		return MemorySnapshot{}, model.ErrConflict
	}
	snapshot.Digest = ""
	got, _, err := memorySnapshotDigest(snapshot)
	snapshot.Digest = digest
	if err != nil || got != digest {
		return MemorySnapshot{}, model.ErrConflict
	}
	for i, rec := range snapshot.Records {
		if snapshot.MemoryIDs[i] != rec.ID || i > 0 && snapshot.Records[i-1].ID >= rec.ID {
			return MemorySnapshot{}, model.ErrConflict
		}
		if err := checkMemorySnapshotRecord(ctx, rec, b); err != nil {
			return MemorySnapshot{}, err
		}
	}
	return snapshot, nil
}
func (s *Store) GetMemorySnapshot(ctx context.Context, b MemorySnapshotBinding, id string, version int64) (MemorySnapshot, error) {
	return loadMemorySnapshot(ctx, s.db, b, id, version)
}

func previewMemoryBody(s string) (string, bool) {
	if len(s) > 512 {
		// Drop a partial final rune. Inserting a replacement rune can grow a
		// 512-byte prefix past the promised preview byte bound.
		return strings.ToValidUTF8(s[:512], ""), true
	}
	return s, false
}
func (s *Store) PreviewMemorySnapshot(ctx context.Context, b MemorySnapshotBinding, id string, version int64, digest, previewID string) (MemorySnapshotPreview, error) {
	if !memorySnapshotIdentifier(previewID) {
		return MemorySnapshotPreview{}, model.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MemorySnapshotPreview{}, err
	}
	defer tx.Rollback()
	snapshot, err := loadMemorySnapshot(ctx, tx, b, id, version)
	if err != nil {
		return MemorySnapshotPreview{}, err
	}
	if snapshot.Digest != digest {
		return MemorySnapshotPreview{}, model.ErrConflict
	}
	current, err := snapshotSelectedRecords(ctx, tx, b, snapshot.MemoryIDs)
	if err != nil {
		return MemorySnapshotPreview{}, err
	}
	stateDigest, _, err := memorySnapshotDigest(current)
	if err != nil {
		return MemorySnapshotPreview{}, err
	}
	preview := MemorySnapshotPreview{ID: previewID, Binding: b, SnapshotID: id, SnapshotVersion: version, SnapshotDigest: digest, CurrentDigest: stateDigest, CreatedAt: time.Now().UTC()}
	for i, rec := range current {
		old := snapshot.Records[i]
		before, beforeTruncated := previewMemoryBody(rec.Body)
		after, afterTruncated := previewMemoryBody(old.Body)
		title, titleTruncated := previewMemoryBody(old.Title)
		preview.Changes = append(preview.Changes, MemoryRestoreChange{MemoryID: rec.ID, CurrentRevision: rec.Revision, SnapshotRevision: old.Revision, BeforeDigest: rec.ContentDigest, AfterDigest: old.ContentDigest, Title: title, BeforeBody: before, AfterBody: after, Truncated: beforeTruncated || afterTruncated || titleTruncated})
	}
	_, raw, err := memorySnapshotDigest(preview)
	if err != nil {
		return MemorySnapshotPreview{}, err
	}
	receiptKey := ""
	if command, ok := ctx.Value(commandKey{}).(CommandRecord); ok {
		receiptKey = command.Key
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO extreme_memory_restore_previews(project_id,preview_id,snapshot_id,version,session_id,actor,command_key,preview_json) VALUES(?,?,?,?,?,?,?,?)`, b.ProjectID, previewID, id, version, b.SessionID, b.Actor, receiptKey, string(raw)); err != nil {
		return MemorySnapshotPreview{}, err
	}
	if err := memorySnapshotCommandReceipt(ctx, tx, b, id, version); err != nil {
		return MemorySnapshotPreview{}, err
	}
	if err := tx.Commit(); err != nil {
		return MemorySnapshotPreview{}, err
	}
	return preview, nil
}

func loadMemoryPreview(ctx context.Context, reader memoryRowReader, b MemorySnapshotBinding, id string) (MemorySnapshotPreview, bool, error) {
	if !validMemorySnapshotBinding(b) || !memorySnapshotIdentifier(id) {
		return MemorySnapshotPreview{}, false, model.ErrInvalid
	}
	var raw string
	var consumed sql.NullString
	err := reader.QueryRowContext(ctx, `SELECT preview_json,consumed_at FROM extreme_memory_restore_previews WHERE project_id=? AND preview_id=? AND session_id=? AND actor=?`, b.ProjectID, id, b.SessionID, b.Actor).Scan(&raw, &consumed)
	if errors.Is(err, sql.ErrNoRows) {
		return MemorySnapshotPreview{}, false, model.ErrNotFound
	}
	if err != nil {
		return MemorySnapshotPreview{}, false, err
	}
	if len(raw) > maxMemorySnapshotBytes {
		return MemorySnapshotPreview{}, false, model.ErrInvalid
	}
	var preview MemorySnapshotPreview
	if err := json.Unmarshal([]byte(raw), &preview); err != nil {
		return preview, false, err
	}
	if preview.ID != id || preview.Binding != b || len(preview.Changes) == 0 || len(preview.Changes) > maxMemorySnapshotRecords {
		return MemorySnapshotPreview{}, false, model.ErrConflict
	}
	return preview, consumed.Valid, nil
}
func (s *Store) GetMemoryPreviewByCommand(ctx context.Context, b MemorySnapshotBinding, key string) (MemorySnapshotPreview, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT preview_id FROM extreme_memory_restore_previews WHERE project_id=? AND session_id=? AND actor=? AND command_key=?`, b.ProjectID, b.SessionID, b.Actor, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return MemorySnapshotPreview{}, model.ErrNotFound
	}
	if err != nil {
		return MemorySnapshotPreview{}, err
	}
	preview, _, err := loadMemoryPreview(ctx, s.db, b, id)
	return preview, err
}

// RestoreMemorySnapshot atomically advances selected existing canonical rows,
// preserves full preimages, consumes the exact preview and writes audit/outbox.
// Current access controls remain in force; restored assertions are candidates.
func (s *Store) RestoreMemorySnapshot(ctx context.Context, b MemorySnapshotBinding, input MemorySnapshotPreview) (MemorySnapshotRestore, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MemorySnapshotRestore{}, err
	}
	defer tx.Rollback()
	preview, consumed, err := loadMemoryPreview(ctx, tx, b, input.ID)
	if err != nil {
		return MemorySnapshotRestore{}, err
	}
	if consumed {
		return MemorySnapshotRestore{}, model.ErrConflict
	}
	inputDigest, _, err := memorySnapshotDigest(input)
	if err != nil {
		return MemorySnapshotRestore{}, err
	}
	storedDigest, _, err := memorySnapshotDigest(preview)
	if err != nil || inputDigest != storedDigest {
		return MemorySnapshotRestore{}, model.ErrConflict
	}
	snapshot, err := loadMemorySnapshot(ctx, tx, b, preview.SnapshotID, preview.SnapshotVersion)
	if err != nil {
		return MemorySnapshotRestore{}, err
	}
	if snapshot.Digest != preview.SnapshotDigest {
		return MemorySnapshotRestore{}, model.ErrConflict
	}
	current, err := snapshotSelectedRecords(ctx, tx, b, snapshot.MemoryIDs)
	if err != nil {
		return MemorySnapshotRestore{}, err
	}
	stateDigest, _, err := memorySnapshotDigest(current)
	if err != nil || stateDigest != preview.CurrentDigest {
		return MemorySnapshotRestore{}, model.ErrConflict
	}
	result := MemorySnapshotRestore{PreviewID: preview.ID, SnapshotID: snapshot.ID, Revisions: map[string]int64{}}
	for i, before := range current {
		target := snapshot.Records[i]
		target.Scope, target.ScopeID, target.ACLScope = before.Scope, before.ScopeID, before.ACLScope
		target.CreatedAt = before.CreatedAt
		target.Lifecycle = model.MemoryCandidate
		target.Confidence = model.ConfidenceUnverified
		target.Authority = model.AuthorityAgent
		target.LastVerifiedAt = nil
		target.Source = model.MemorySource{Kind: "memory_restore", Reference: snapshot.ID, AgentID: b.Actor, SessionID: b.SessionID}
		target.SessionID = b.SessionID
		after, err := s.updateMemoryTx(ctx, tx, memorySnapshotProject(b), before.ID, before.Revision, func(rec *model.MemoryRecordV2) error { *rec = target; return nil })
		if err != nil {
			return MemorySnapshotRestore{}, err
		}
		_, beforeJSON, err := memorySnapshotDigest(before)
		if err != nil {
			return MemorySnapshotRestore{}, err
		}
		_, afterJSON, err := memorySnapshotDigest(after)
		if err != nil {
			return MemorySnapshotRestore{}, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO extreme_memory_restore_history(project_id,preview_id,memory_id,before_json,after_json,restored_at) VALUES(?,?,?,?,?,?)`, b.ProjectID, preview.ID, before.ID, string(beforeJSON), string(afterJSON), utcNow()); err != nil {
			return MemorySnapshotRestore{}, err
		}
		result.Revisions[before.ID] = after.Revision
	}
	if _, err := tx.ExecContext(ctx, `UPDATE extreme_memory_restore_previews SET consumed_at=? WHERE project_id=? AND preview_id=? AND consumed_at IS NULL`, utcNow(), b.ProjectID, preview.ID); err != nil {
		return MemorySnapshotRestore{}, err
	}
	if err := memorySnapshotCommandReceipt(ctx, tx, b, snapshot.ID, snapshot.Version+1); err != nil {
		return MemorySnapshotRestore{}, err
	}
	if err := memorySnapshotPrecommit(ctx); err != nil {
		return MemorySnapshotRestore{}, err
	}
	if err := tx.Commit(); err != nil {
		return MemorySnapshotRestore{}, err
	}
	return result, nil
}

func (s *Store) GetMemoryRestoreHistory(ctx context.Context, b MemorySnapshotBinding, previewID string) ([]MemoryRestoreHistory, error) {
	preview, consumed, err := loadMemoryPreview(ctx, s.db, b, previewID)
	if err != nil {
		return nil, err
	}
	if !consumed {
		return nil, model.ErrConflict
	}
	rows, err := s.db.QueryContext(ctx, `SELECT memory_id,before_json,after_json FROM extreme_memory_restore_history WHERE project_id=? AND preview_id=? ORDER BY memory_id`, b.ProjectID, preview.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var history []MemoryRestoreHistory
	total := 0
	for rows.Next() {
		var item MemoryRestoreHistory
		var before, after string
		if err := rows.Scan(&item.MemoryID, &before, &after); err != nil {
			return nil, err
		}
		total += len(before) + len(after)
		if len(history) >= maxMemorySnapshotRecords || total > 2*maxMemorySnapshotBytes {
			return nil, model.ErrInvalid
		}
		if err := json.Unmarshal([]byte(before), &item.Before); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(after), &item.After); err != nil {
			return nil, err
		}
		if err := checkMemorySnapshotRecord(ctx, item.Before, b); err != nil {
			return nil, err
		}
		if err := checkMemorySnapshotRecord(ctx, item.After, b); err != nil {
			return nil, err
		}
		history = append(history, item)
	}
	return history, rows.Err()
}
func (s *Store) GetMemoryRestoreResult(ctx context.Context, b MemorySnapshotBinding, id string) (MemorySnapshotRestore, error) {
	history, err := s.GetMemoryRestoreHistory(ctx, b, id)
	if err != nil {
		return MemorySnapshotRestore{}, err
	}
	preview, _, err := loadMemoryPreview(ctx, s.db, b, id)
	if err != nil {
		return MemorySnapshotRestore{}, err
	}
	result := MemorySnapshotRestore{PreviewID: id, SnapshotID: preview.SnapshotID, Revisions: map[string]int64{}}
	for _, item := range history {
		result.Revisions[item.MemoryID] = item.After.Revision
	}
	return result, nil
}

func memorySnapshotCommandReceipt(ctx context.Context, tx *sql.Tx, b MemorySnapshotBinding, id string, version int64) error {
	command, ok := ctx.Value(commandKey{}).(CommandRecord)
	if !ok {
		return nil
	} // Store APIs are trusted composition; application APIs authenticate.
	if command.ProjectID != b.ProjectID || command.SessionID != b.SessionID || command.Actor != b.Actor || command.TargetID != "memory-snapshot:"+id {
		return authz.ErrDenied
	}
	return insertCommandReceipt(ctx, tx, command, version)
}

type memorySnapshotPrecommitKey struct{}

// WithMemorySnapshotPrecommit binds the runtime's fixed read-only HEAD check to
// the transaction. It cannot publish a mutation or bypass stored CAS checks.
func WithMemorySnapshotPrecommit(ctx context.Context, check func(context.Context) error) context.Context {
	return context.WithValue(ctx, memorySnapshotPrecommitKey{}, check)
}
func memorySnapshotPrecommit(ctx context.Context) error {
	if check, ok := ctx.Value(memorySnapshotPrecommitKey{}).(func(context.Context) error); ok {
		return check(ctx)
	}
	return ctx.Err()
}
