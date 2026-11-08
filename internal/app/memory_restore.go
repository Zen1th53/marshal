package app

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

type MemoryRestoreRequest struct {
	SnapshotID     string `json:"snapshot_id"`
	SnapshotDigest string `json:"snapshot_digest"`
	StateDigest    string `json:"state_digest"`
	PolicyDigest   string `json:"policy_digest"`
	Reason         string `json:"reason"`
}
type MemoryRestoreReview struct {
	Snapshot     model.MemoryRestoreSnapshot
	Generation   int64
	StateDigest  string
	PolicyDigest string
}

// CreateMemorySnapshot persists authenticated encrypted payloads for a bounded
// selected project record set. This is deliberately distinct from versioning's
// in-memory revision-only branch snapshots.
func (r *Runtime) CreateMemorySnapshot(ctx context.Context, e CommandEnvelope, name string, ids []string) (model.MemoryRestoreSnapshot, error) {
	projectID := e.ProjectID
	if projectID == "" {
		projectID = r.ProjectIdentity()
		if projectID == "" {
			projectID = r.ProjectID()
		}
	}
	targetProject := projectID
	if len(ids) > 0 {
		if _, err := r.store.GetMemoryV2(ctx, projectID, ids[0]); err != nil && projectID != r.ProjectID() {
			if _, err2 := r.store.GetMemoryV2(ctx, r.ProjectID(), ids[0]); err2 == nil {
				targetProject = r.ProjectID()
			}
		}
	} else {
		recs, listErr := r.store.ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: projectID, Limit: 128})
		if listErr == nil && len(recs) > 0 {
			for _, rec := range recs {
				if validateMemorySelection(rec, projectID) == nil {
					ids = append(ids, rec.ID)
				}
			}
		}
		if len(ids) == 0 && projectID != r.ProjectID() {
			recs, listErr = r.store.ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: r.ProjectID(), Limit: 128})
			if listErr == nil && len(recs) > 0 {
				for _, rec := range recs {
					if validateMemorySelection(rec, r.ProjectID()) == nil {
						ids = append(ids, rec.ID)
					}
				}
				if len(ids) > 0 {
					targetProject = r.ProjectID()
				}
			}
		}
	}
	ctx, p, _, err := r.memoryCommand(ctx, e, "memory.snapshot", struct {
		Name string
		IDs  []string
	}{Name: name, IDs: ids})
	if err != nil {
		return model.MemoryRestoreSnapshot{}, err
	}
	if name == "" || len(name) > 256 || len(ids) == 0 || len(ids) > 128 {
		return model.MemoryRestoreSnapshot{}, model.ErrInvalid
	}
	state, err := r.store.MemoryRestoreState(ctx, targetProject)
	if err != nil {
		return model.MemoryRestoreSnapshot{}, err
	}
	sort.Strings(ids)
	seen := map[string]bool{}
	var records []model.MemoryRecordV2
	snap := model.MemoryRestoreSnapshot{ID: e.TargetID, ProjectID: targetProject, Name: name, PolicyDigest: r.memoryPolicyDigest(), StateDigest: state.Digest, CreatedAt: time.Now().UTC()}
	for _, id := range ids {
		if seen[id] {
			return snap, model.ErrInvalid
		}
		seen[id] = true
		rec, err := r.memoryService.Get(ctx, p, targetProject, id)
		if err != nil {
			return snap, err
		}
		if err = validateMemorySelection(rec, targetProject); err != nil {
			return snap, err
		}
		if err = r.memoryService.revalidateMemoryRecord(ctx, rec, MemoryReconcileRequest{}); err != nil {
			return snap, err
		}
		records = append(records, rec)
		snap.Records = append(snap.Records, model.MemorySnapshotRecord{ID: rec.ID, Revision: rec.Revision, Digest: rec.ContentDigest})
	}
	snap.Seal()
	payload, err := json.Marshal(records)
	if err != nil {
		return snap, err
	}
	protected, err := r.protectMemorySnapshot(payload, []byte(snap.Digest), true)
	if err != nil {
		return snap, err
	}
	if err = r.store.SaveMemoryRestoreSnapshot(ctx, snap, protected); err != nil {
		return snap, err
	}
	got, _, err := r.store.GetMemoryRestoreSnapshot(ctx, targetProject, snap.ID)
	return got, err
}

func (r *Runtime) ReviewMemoryRestore(ctx context.Context, id string) (MemoryRestoreReview, error) {
	var review MemoryRestoreReview
	p, ok := auth.LocalFromContext(ctx)
	if r == nil || !ok || ctx.Value(localControlKey{}) != r || p.ProjectID() != r.ProjectIdentity() {
		return review, authz.ErrDenied
	}
	projectID := r.ProjectIdentity()
	if projectID == "" {
		projectID = r.ProjectID()
	}
	snap, _, err := r.store.GetMemoryRestoreSnapshot(ctx, projectID, id)
	if err != nil && projectID != r.ProjectID() {
		var err2 error
		snap, _, err2 = r.store.GetMemoryRestoreSnapshot(ctx, r.ProjectID(), id)
		if err2 == nil {
			projectID = r.ProjectID()
			err = nil
		}
	}
	if err != nil {
		return review, err
	}
	state, err := r.store.MemoryRestoreState(ctx, projectID)
	if err != nil {
		return review, err
	}
	return MemoryRestoreReview{Snapshot: snap, Generation: state.Generation, StateDigest: state.Digest, PolicyDigest: r.memoryPolicyDigest()}, nil
}
func (r *Runtime) MemorySnapshots(ctx context.Context) ([]model.MemoryRestoreSnapshot, error) {
	p, ok := auth.LocalFromContext(ctx)
	if r == nil || !ok || ctx.Value(localControlKey{}) != r || p.ProjectID() != r.ProjectIdentity() {
		return nil, authz.ErrDenied
	}
	projectID := r.ProjectIdentity()
	if projectID == "" {
		projectID = r.ProjectID()
	}
	snaps, err := r.store.ListMemoryRestoreSnapshots(ctx, projectID)
	if (err != nil || len(snaps) == 0) && projectID != r.ProjectID() {
		if snaps2, err2 := r.store.ListMemoryRestoreSnapshots(ctx, r.ProjectID()); err2 == nil && len(snaps2) > 0 {
			return snaps2, nil
		}
	}
	return snaps, err
}

func (r *Runtime) CommandRestoreMemory(ctx context.Context, e CommandEnvelope, req MemoryRestoreRequest) (model.MemoryRestoreResult, error) {
	var empty model.MemoryRestoreResult
	if req.SnapshotID != e.TargetID || req.SnapshotDigest == "" || req.StateDigest == "" || req.PolicyDigest == "" || req.Reason == "" || len(req.Reason) > 2048 {
		return empty, model.ErrInvalid
	}
	ctx, p, command, err := r.memoryCommand(ctx, e, "memory.restore", req)
	if err != nil {
		return empty, err
	}
	projectID := e.ProjectID
	if projectID == "" {
		projectID = r.ProjectIdentity()
		if projectID == "" {
			projectID = r.ProjectID()
		}
	}
	if req.PolicyDigest != r.memoryPolicyDigest() {
		return empty, fmt.Errorf("%w: memory restore policy changed", model.ErrConflict)
	}
	if version, found, err := r.store.MemoryCommandResult(ctx, command); err != nil {
		return empty, err
	} else if found {
		res, err := r.store.MemoryRestoreResult(ctx, projectID, version)
		if err != nil && projectID != r.ProjectID() {
			res, err = r.store.MemoryRestoreResult(ctx, r.ProjectID(), version)
		}
		return res, err
	}
	snap, protected, err := r.store.GetMemoryRestoreSnapshot(ctx, projectID, req.SnapshotID)
	if err != nil && projectID != r.ProjectID() {
		var err2 error
		snap, protected, err2 = r.store.GetMemoryRestoreSnapshot(ctx, r.ProjectID(), req.SnapshotID)
		if err2 == nil {
			projectID = r.ProjectID()
			err = nil
		}
	}
	if err != nil {
		return empty, err
	}
	if snap.Digest != req.SnapshotDigest || snap.PolicyDigest != req.PolicyDigest {
		return empty, model.ErrConflict
	}
	state, err := r.store.MemoryRestoreState(ctx, snap.ProjectID)
	if err != nil {
		return empty, err
	}
	if state.Generation != e.ExpectedVersion || state.Digest != req.StateDigest {
		return empty, model.ErrConflict
	}
	payload, err := r.protectMemorySnapshot(protected, []byte(snap.Digest), false)
	if err != nil {
		return empty, err
	}
	var records []model.MemoryRecordV2
	if err = json.Unmarshal(payload, &records); err != nil {
		return empty, err
	}
	for _, rec := range records {
		if err = validateMemorySelection(rec, snap.ProjectID); err != nil {
			return empty, err
		}
		if _, err = r.memoryService.Get(ctx, p, snap.ProjectID, rec.ID); err != nil {
			return empty, err
		}
		if err = r.memoryService.revalidateMemoryRecord(ctx, rec, MemoryReconcileRequest{}); err != nil {
			return empty, err
		}
	}
	result, err := r.store.ApplyMemoryRestore(ctx, snap, state, records)
	if err != nil {
		return empty, err
	}
	// Projection failure does not roll back committed canonical state. The
	// durable outbox remains available for restart recovery; report that truth.
	if err = r.memoryService.RebuildProjections(ctx, snap.ProjectID); err != nil {
		result.ProjectionStatus = "PENDING: canonical restore committed; projection recovery required"
	} else {
		result.ProjectionStatus = "REBUILT: canonical records reloaded"
	}
	return result, nil
}

func (r *Runtime) protectMemorySnapshot(data, aad []byte, encrypt bool) ([]byte, error) {
	keyPath := filepath.Join(r.layout.RuntimeDir, "memory-restore.key")
	root, err := os.OpenRoot(r.layout.RuntimeDir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	info, err := root.Lstat("memory-restore.key")
	if os.IsNotExist(err) && encrypt {
		key := make([]byte, 32)
		if _, err = io.ReadFull(rand.Reader, key); err != nil {
			return nil, err
		}
		f, createErr := root.OpenFile("memory-restore.key", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if createErr == nil {
			_, err = f.Write(key)
			if err == nil {
				err = f.Sync()
			}
			closeErr := f.Close()
			if err != nil {
				return nil, err
			}
			if closeErr != nil {
				return nil, closeErr
			}
		} else if !os.IsExist(createErr) {
			return nil, createErr
		}
		info, err = root.Lstat("memory-restore.key")
	}
	if err != nil {
		return nil, fmt.Errorf("memory restore key unavailable: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return nil, fmt.Errorf("%w: insecure memory restore key %s", authz.ErrDenied, filepath.Base(keyPath))
	}
	f, err := root.Open("memory-restore.key")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, authz.ErrDenied
	}
	key, err := io.ReadAll(io.LimitReader(f, 33))
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("%w: invalid memory restore key", model.ErrInvalid)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if encrypt {
		nonce := make([]byte, gcm.NonceSize())
		if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
			return nil, err
		}
		return gcm.Seal(nonce, nonce, data, aad), nil
	}
	if len(data) < gcm.NonceSize() {
		return nil, model.ErrInvalid
	}
	return gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], aad)
}
