package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
)

func memoryRestoreStateTx(ctx context.Context, tx *sql.Tx, project string) (model.MemoryRestoreState, error) {
	state := model.MemoryRestoreState{ProjectID: project}
	err := tx.QueryRowContext(ctx, `SELECT generation FROM memory_restore_heads WHERE project_id=?`, project).Scan(&state.Generation)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return state, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT memory_id FROM memory_records_v2 WHERE project_id=? ORDER BY memory_id LIMIT 4097`, project)
	if err != nil {
		return state, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return state, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return state, err
	}
	if len(ids) > 4096 {
		return state, fmt.Errorf("%w: memory restore state exceeds bound", model.ErrInvalid)
	}
	for _, id := range ids {
		rec, err := getMemoryV2(ctx, tx, project, id)
		if err != nil {
			return state, err
		}
		state.Records = append(state.Records, rec)
	}
	body, err := json.Marshal(struct {
		Project    string
		Generation int64
		Records    []model.MemoryRecordV2
	}{project, state.Generation, state.Records})
	if err != nil {
		return state, err
	}
	sum := sha256.Sum256(body)
	state.Digest = hex.EncodeToString(sum[:])
	return state, nil
}

func (s *Store) MemoryRestoreState(ctx context.Context, project string) (model.MemoryRestoreState, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.MemoryRestoreState{}, err
	}
	defer tx.Rollback()
	return memoryRestoreStateTx(ctx, tx, project)
}
func (s *Store) SaveMemoryRestoreSnapshot(ctx context.Context, snap model.MemoryRestoreSnapshot, payload []byte) error {
	if !snap.Valid() || len(payload) == 0 || len(payload) > 16<<20 {
		return model.ErrInvalid
	}
	body, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := memoryRestoreStateTx(ctx, tx, snap.ProjectID)
	if err != nil {
		return err
	}
	if current.Digest != snap.StateDigest {
		return model.ErrConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO memory_restore_snapshots(project_id,snapshot_id,manifest_digest,manifest_json,payload,created_at) VALUES(?,?,?,?,?,?)`, snap.ProjectID, snap.ID, snap.Digest, body, payload, snap.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) GetMemoryRestoreSnapshot(ctx context.Context, project, id string) (model.MemoryRestoreSnapshot, []byte, error) {
	var body, payload []byte
	var digest string
	err := s.db.QueryRowContext(ctx, `SELECT manifest_json,payload,manifest_digest FROM memory_restore_snapshots WHERE project_id=? AND snapshot_id=?`, project, id).Scan(&body, &payload, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return model.MemoryRestoreSnapshot{}, nil, model.ErrNotFound
	}
	if err != nil {
		return model.MemoryRestoreSnapshot{}, nil, err
	}
	var snap model.MemoryRestoreSnapshot
	if err = json.Unmarshal(body, &snap); err != nil {
		return snap, nil, err
	}
	if !snap.Valid() || snap.ProjectID != project || snap.ID != id || snap.Digest != digest {
		return snap, nil, model.ErrConflict
	}
	return snap, payload, nil
}
func (s *Store) ListMemoryRestoreSnapshots(ctx context.Context, project string) ([]model.MemoryRestoreSnapshot, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT manifest_json FROM memory_restore_snapshots WHERE project_id=? ORDER BY created_at DESC,snapshot_id LIMIT 128`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.MemoryRestoreSnapshot
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var snap model.MemoryRestoreSnapshot
		if err := json.Unmarshal(body, &snap); err != nil {
			return nil, err
		}
		if !snap.Valid() || snap.ProjectID != project {
			return nil, model.ErrConflict
		}
		out = append(out, snap)
	}
	return out, rows.Err()
}

// ApplyMemoryRestore rechecks the complete current-state digest inside the same
// transaction as every record CAS, history, outbox and command audit. Historical
// payloads return as untrusted candidates; current ACL/evidence/provenance and
// temporal restrictions are retained. Missing/revoked/protected records refuse
// the entire transaction rather than being reconstructed from a snapshot.
func (s *Store) ApplyMemoryRestore(ctx context.Context, snap model.MemoryRestoreSnapshot, expected model.MemoryRestoreState, payloads []model.MemoryRecordV2) (model.MemoryRestoreResult, error) {
	var result model.MemoryRestoreResult
	command, ok := ctx.Value(commandKey{}).(CommandRecord)
	if !ok || command.Operation != "memory.restore" || (command.ProjectID != snap.ProjectID && command.TargetProjectID != snap.ProjectID) || command.TargetID != snap.ID || command.ExpectedVersion != expected.Generation || !snap.Valid() || len(payloads) != len(snap.Records) {
		return result, model.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	state, err := memoryRestoreStateTx(ctx, tx, snap.ProjectID)
	if err != nil {
		return result, err
	}
	if state.ProjectID != expected.ProjectID || state.Generation != expected.Generation || state.Digest != expected.Digest {
		return result, model.ErrConflict
	}
	var durableDigest string
	if err = tx.QueryRowContext(ctx, `SELECT manifest_digest FROM memory_restore_snapshots WHERE project_id=? AND snapshot_id=?`, snap.ProjectID, snap.ID).Scan(&durableDigest); err != nil {
		return result, err
	}
	if durableDigest != snap.Digest {
		return result, model.ErrConflict
	}
	current := map[string]model.MemoryRecordV2{}
	for _, r := range state.Records {
		current[r.ID] = r
	}
	seen := map[string]bool{}
	result = model.MemoryRestoreResult{SnapshotID: snap.ID, SnapshotDigest: snap.Digest, Generation: state.Generation + 1, ProjectionStatus: "PENDING: durable outbox requires projection recovery"}
	now := time.Now().UTC()
	for i, old := range payloads {
		entry := snap.Records[i]
		live, found := current[entry.ID]
		if !found || seen[entry.ID] || old.ID != entry.ID || old.ProjectID != snap.ProjectID || old.Revision != entry.Revision || old.ContentDigest != entry.Digest || old.CanonicalDigest() != entry.Digest {
			return result, model.ErrConflict
		}
		seen[entry.ID] = true
		if !restorableMemoryRecord(live, now) || !restorableMemoryRecord(old, now) || live.Scope != old.Scope || live.ScopeID != old.ScopeID || live.ACLScope != old.ACLScope {
			return result, authz.ErrDenied
		}
		for _, ev := range append(append([]string{}, old.EvidenceIDs...), live.EvidenceIDs...) {
			var status string
			if err := tx.QueryRowContext(ctx, `SELECT state FROM evidence_nodes WHERE node_id=?`, ev).Scan(&status); err != nil || (status != "stored" && status != "linked") {
				return result, fmt.Errorf("%w: restore evidence unavailable", model.ErrConflict)
			}
		}
		rec, err := s.updateMemoryTx(ctx, tx, snap.ProjectID, live.ID, live.Revision, func(r *model.MemoryRecordV2) error {
			r.Title = old.Title
			r.Body = old.Body
			r.Kind = old.Kind
			r.Lifecycle = model.MemoryCandidate
			r.Authority = model.AuthorityAgent
			r.Confidence = model.ConfidenceUnverified
			r.LastVerifiedAt = nil
			if r.ExtMeta == nil {
				r.ExtMeta = map[string]any{}
			}
			r.ExtMeta["restore_snapshot"] = snap.ID
			r.ExtMeta["restore_generation"] = result.Generation
			return nil
		})
		if err != nil {
			return result, err
		}
		result.Restored = append(result.Restored, model.MemorySnapshotRecord{ID: rec.ID, Revision: rec.Revision, Digest: rec.ContentDigest})
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO memory_restore_heads(project_id,generation) VALUES(?,?) ON CONFLICT(project_id) DO UPDATE SET generation=excluded.generation`, snap.ProjectID, result.Generation)
	if err != nil {
		return result, err
	}
	after, err := memoryRestoreStateTx(ctx, tx, snap.ProjectID)
	if err != nil {
		return result, err
	}
	result.StateDigest = after.Digest
	body, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO memory_restore_history(project_id,generation,snapshot_id,snapshot_digest,result_json,created_at) VALUES(?,?,?,?,?,?)`, snap.ProjectID, result.Generation, snap.ID, snap.Digest, body, utcNow())
	if err != nil {
		return result, err
	}
	if err = insertCommandReceipt(ctx, tx, command, result.Generation); err != nil {
		return result, err
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}
func restorableMemoryRecord(r model.MemoryRecordV2, now time.Time) bool {
	if r.Scope != "project" || r.ScopeID != r.ProjectID || r.ACLScope != "" || len(r.SupersededBy) > 0 || len(r.ConflictIDs) > 0 || now.Before(r.ValidFrom) || (r.ValidTo != nil && !now.Before(*r.ValidTo)) {
		return false
	}
	return r.Lifecycle == model.MemoryCandidate || r.Lifecycle == model.MemoryVerified || r.Lifecycle == model.MemoryDurable
}
func (s *Store) MemoryRestoreResult(ctx context.Context, project string, generation int64) (model.MemoryRestoreResult, error) {
	var result model.MemoryRestoreResult
	var body []byte
	err := s.db.QueryRowContext(ctx, `SELECT result_json FROM memory_restore_history WHERE project_id=? AND generation=?`, project, generation).Scan(&body)
	if err != nil {
		return result, err
	}
	err = json.Unmarshal(body, &result)
	return result, err
}
