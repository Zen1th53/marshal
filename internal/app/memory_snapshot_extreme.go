//go:build !governed

package app

import (
	"context"
	"strings"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

// memorySnapshotCommand derives identity from trusted LocalControl, validates
// the actual project's canonical goal session, and reuses existing scoped
// checkpoint/state capabilities. No principal or role is accepted on the wire.
func (r *Runtime) memorySnapshotCommand(ctx context.Context, e CommandEnvelope, action string, input any) (store.CommandRecord, bool, error) {
	record, found, err := r.checkpointCommand(ctx, e, action, input)
	if err != nil {
		return record, false, err
	}
	id, ok := strings.CutPrefix(e.TargetID, "memory-snapshot:")
	if !ok || id == "" || len(id) > 256 {
		return store.CommandRecord{}, false, model.ErrInvalid
	}
	goal, err := r.store.GetActiveGoalContract(ctx, e.SessionID)
	if err != nil {
		return store.CommandRecord{}, false, authz.ErrDenied
	}
	if goal.ProjectID != e.ProjectID || goal.SessionID != e.SessionID {
		return store.CommandRecord{}, false, authz.ErrDenied
	}
	return record, found, nil
}

func (r *Runtime) memorySnapshotHead(ctx context.Context, expected string) error {
	if !fullProofObject(expected) {
		return model.ErrInvalid
	}
	head, err := runtimeProofGit(ctx, r.ProjectRoot(), "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if strings.TrimSpace(head) != expected {
		return model.ErrConflict
	}
	return nil
}

func memorySnapshotMetadata(snapshot store.MemorySnapshot) store.MemorySnapshot {
	snapshot.Records = nil
	return snapshot
}

// MemorySnapshots discovers retained selected-record snapshots after restart.
// It exposes bounded metadata only to the exact authorized local goal session.
func (r *Runtime) RetainedMemorySnapshots(ctx context.Context, projectID, sessionID string, limit int) ([]store.MemorySnapshot, error) {
	p, ok := auth.LocalFromContext(ctx)
	if r == nil || r.store == nil || !ok || ctx.Value(localControlKey{}) != r || p.ProjectID() != projectID || projectID != r.ProjectIdentity() {
		return nil, authz.ErrDenied
	}
	if limit < 1 || limit > 50 {
		return nil, model.ErrInvalid
	}
	goal, err := r.store.GetActiveGoalContract(ctx, sessionID)
	if err != nil || goal.ProjectID != projectID || goal.SessionID != sessionID {
		return nil, authz.ErrDenied
	}
	principal := authz.Principal{ID: p.ID(), Role: authz.Role{Name: "orchestrator", Authorities: []authz.Authority{authz.AuthorityTaskPlan}}}
	query := capability.Query{Subject: capability.SubjectID(p.ID()), TaskID: capability.TaskID(projectID), Kind: capability.KindFilesystemWrite, Resource: r.layout.Database, Action: "state.restore"}
	if _, err := authz.CanWithCapability(ctx, principal, authz.AuthorityTaskPlan, r.layout.Database, query, capability.NewEngine(r.store, nil)); err != nil {
		return nil, err
	}
	head, err := runtimeProofGit(ctx, r.ProjectRoot(), "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	b, err := r.memorySnapshotBinding(ctx, CommandEnvelope{ProjectID: projectID, SessionID: sessionID}, p.ID(), strings.TrimSpace(head))
	if err != nil {
		return nil, err
	}
	return r.store.ListMemorySnapshots(ctx, b, limit)
}

// CommandCreateMemorySnapshot captures only the explicitly selected canonical
// records. The result exposes count/identity metadata; full bodies stay durable.
func (r *Runtime) CommandCreateMemorySnapshot(ctx context.Context, e CommandEnvelope, head string, ids []string) (store.MemorySnapshot, error) {
	record, found, err := r.memorySnapshotCommand(ctx, e, "checkpoint.create", struct {
		Head string
		IDs  []string
	}{head, ids})
	if err != nil {
		return store.MemorySnapshot{}, err
	}
	if e.ExpectedVersion != 0 {
		return store.MemorySnapshot{}, model.ErrConflict
	}
	id := strings.TrimPrefix(e.TargetID, "memory-snapshot:")
	b, err := r.memorySnapshotBinding(ctx, e, record.Actor, head)
	if err != nil {
		return store.MemorySnapshot{}, err
	}
	if found {
		snapshot, err := r.store.GetMemorySnapshot(ctx, b, id, 1)
		return memorySnapshotMetadata(snapshot), err
	}
	if err := r.memorySnapshotHead(ctx, head); err != nil {
		return store.MemorySnapshot{}, err
	}
	snapshot, err := r.store.CreateMemorySnapshot(store.WithCommand(ctx, record), b, id, ids)
	return memorySnapshotMetadata(snapshot), err
}

// CommandPreviewMemorySnapshot issues a durable preview of the exact snapshot
// version, current full-record states and repository HEAD. Body excerpts are
// bounded and are exposed only after authenticated project/session binding.
func (r *Runtime) CommandPreviewMemorySnapshot(ctx context.Context, e CommandEnvelope, digest, head string) (store.MemorySnapshotPreview, error) {
	record, found, err := r.memorySnapshotCommand(ctx, e, "state.restore", struct{ Digest, Head string }{digest, head})
	if err != nil {
		return store.MemorySnapshotPreview{}, err
	}
	if e.ExpectedVersion != 1 {
		return store.MemorySnapshotPreview{}, model.ErrConflict
	}
	b, err := r.memorySnapshotBinding(ctx, e, record.Actor, head)
	if err != nil {
		return store.MemorySnapshotPreview{}, err
	}
	if found {
		return r.store.GetMemoryPreviewByCommand(ctx, b, record.Key)
	}
	if err := r.memorySnapshotHead(ctx, head); err != nil {
		return store.MemorySnapshotPreview{}, err
	}
	id, err := model.NewID("MPREVIEW-")
	if err != nil {
		return store.MemorySnapshotPreview{}, err
	}
	return r.store.PreviewMemorySnapshot(store.WithCommand(ctx, record), b, strings.TrimPrefix(e.TargetID, "memory-snapshot:"), e.ExpectedVersion, digest, id)
}

// CommandRestoreMemorySnapshot performs one authenticated, exact-preview CAS
// transaction. It preserves access controls and prior bodies and writes new
// candidate revisions; independent verification/promotion remains required.
func (r *Runtime) CommandRestoreMemorySnapshot(ctx context.Context, e CommandEnvelope, preview store.MemorySnapshotPreview) (store.MemorySnapshotRestore, error) {
	record, found, err := r.memorySnapshotCommand(ctx, e, "state.restore", preview)
	if err != nil {
		return store.MemorySnapshotRestore{}, err
	}
	if e.ExpectedVersion != preview.SnapshotVersion || preview.SnapshotID != strings.TrimPrefix(e.TargetID, "memory-snapshot:") || preview.Binding.ProjectID != e.ProjectID || preview.Binding.SessionID != e.SessionID || preview.Binding.Actor != record.Actor {
		return store.MemorySnapshotRestore{}, model.ErrConflict
	}
	b, err := r.memorySnapshotBinding(ctx, e, record.Actor, preview.Binding.HeadCommit)
	if err != nil {
		return store.MemorySnapshotRestore{}, err
	}
	if b != preview.Binding {
		return store.MemorySnapshotRestore{}, model.ErrConflict
	}
	if found {
		return r.store.GetMemoryRestoreResult(ctx, b, preview.ID)
	}
	if err := r.memorySnapshotHead(ctx, b.HeadCommit); err != nil {
		return store.MemorySnapshotRestore{}, err
	}
	ctx = store.WithMemorySnapshotPrecommit(store.WithCommand(ctx, record), func(ctx context.Context) error { return r.memorySnapshotHead(ctx, b.HeadCommit) })
	result, err := r.store.RestoreMemorySnapshot(ctx, b, preview)
	if err != nil {
		return result, err
	}
	// Canonical writes/outbox/audit already committed. Disposable projections
	// must never alter the authoritative restore result or fake a rollback.
	if r.memoryService != nil {
		for id := range result.Revisions {
			if rec, getErr := r.store.GetMemoryV2(ctx, b.StorageProjectID, id); getErr == nil {
				_ = r.memoryService.InvalidateRecord(ctx, b.StorageProjectID, id, rec.ScopeID)
				_ = r.memoryService.IndexRecord(ctx, rec)
			}
		}
	}
	return result, nil
}

// Canonical authentication identity and the historical SQLite project key are
// deliberately separate. Only runtime composition can resolve this mapping.
func (r *Runtime) memorySnapshotBinding(ctx context.Context, e CommandEnvelope, actor, head string) (store.MemorySnapshotBinding, error) {
	project, err := r.store.Project(ctx)
	if err != nil {
		return store.MemorySnapshotBinding{}, err
	}
	return store.MemorySnapshotBinding{ProjectID: e.ProjectID, StorageProjectID: project.ID, SessionID: e.SessionID, Actor: actor, HeadCommit: head}, nil
}
