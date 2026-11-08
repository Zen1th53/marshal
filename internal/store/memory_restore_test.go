package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func memoryRestoreFixture(t *testing.T) (*Store, model.MemoryRecordV2) {
	t.Helper()
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	// Provisional v92 service fixture, not migration registration qualification.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = migrateMemoryRestore(ctx, tx); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = s.InitProject(ctx, model.Project{ID: "memory-restore", Repository: "scratch", DefaultBranch: "main", PackVersion: "1"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(-time.Second)
	rec := model.MemoryRecordV2{ID: "memory-one", ProjectID: "memory-restore", Kind: model.MemoryKindSemantic, Lifecycle: model.MemoryCandidate, Confidence: model.ConfidenceInferred, Authority: model.AuthorityAgent, Title: "old", Body: "historical content", Scope: "project", ScopeID: "memory-restore", Source: model.MemorySource{Kind: "runtime_service", Reference: "operator"}, ObservedAt: now, IngestedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now}
	if err = s.WriteMemoryV2(ctx, rec); err != nil {
		t.Fatal(err)
	}
	rec, err = s.GetMemoryV2(ctx, rec.ProjectID, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s, rec
}

func TestMemoryRestorePersistsSnapshotPayloads(t *testing.T) {
	s, rec := memoryRestoreFixture(t)
	ctx := context.Background()
	state, err := s.MemoryRestoreState(ctx, rec.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	snap := model.MemoryRestoreSnapshot{ID: "snapshot-one", ProjectID: rec.ProjectID, Name: "before", PolicyDigest: "policy", StateDigest: state.Digest, CreatedAt: time.Now().UTC(), Records: []model.MemorySnapshotRecord{{ID: rec.ID, Revision: rec.Revision, Digest: rec.ContentDigest}}}
	snap.Seal()
	if err = s.SaveMemoryRestoreSnapshot(ctx, snap, []byte("protected-payload")); err != nil {
		t.Fatal(err)
	}
	got, payload, err := s.GetMemoryRestoreSnapshot(ctx, rec.ProjectID, snap.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Digest != snap.Digest || string(payload) != "protected-payload" {
		t.Fatal("durable snapshot lost its payload")
	}
	if err = s.SaveMemoryRestoreSnapshot(ctx, snap, []byte("replacement")); err == nil {
		t.Fatal("immutable snapshot overwritten")
	}
}

func TestMemoryRestoreAtomicCASAndAudit(t *testing.T) {
	s, rec := memoryRestoreFixture(t)
	ctx := context.Background()
	state, err := s.MemoryRestoreState(ctx, rec.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	snap := model.MemoryRestoreSnapshot{ID: "snapshot-one", ProjectID: rec.ProjectID, Name: "before", PolicyDigest: "policy", StateDigest: state.Digest, CreatedAt: time.Now().UTC(), Records: []model.MemorySnapshotRecord{{ID: rec.ID, Revision: rec.Revision, Digest: rec.ContentDigest}}}
	snap.Seal()
	if err = s.SaveMemoryRestoreSnapshot(ctx, snap, []byte("protected")); err != nil {
		t.Fatal(err)
	}
	current, err := s.UpdateMemory(ctx, rec.ProjectID, rec.ID, rec.Revision, func(r *model.MemoryRecordV2) error { r.Body = "later content"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	state, err = s.MemoryRestoreState(ctx, rec.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	command := CommandRecord{ProjectID: rec.ProjectID, Actor: "operator", Key: "restore-once", Operation: "memory.restore", SessionID: "session", TargetID: snap.ID, ExpectedVersion: state.Generation, Digest: "intent"}
	if _, err = s.db.ExecContext(ctx, `CREATE TRIGGER fail_restore_audit BEFORE INSERT ON command_audit BEGIN SELECT RAISE(ABORT,'audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	_, err = s.ApplyMemoryRestore(WithCommand(ctx, command), snap, state, []model.MemoryRecordV2{rec})
	if err == nil {
		t.Fatal("audit failure committed restore")
	}
	got, err := s.GetMemoryV2(ctx, rec.ProjectID, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != current.Revision || got.Body != current.Body {
		t.Fatal("failed transaction changed canonical memory")
	}
	if _, err = s.db.ExecContext(ctx, `DROP TRIGGER fail_restore_audit`); err != nil {
		t.Fatal(err)
	}
	result, err := s.ApplyMemoryRestore(WithCommand(ctx, command), snap, state, []model.MemoryRecordV2{rec})
	if err != nil {
		t.Fatal(err)
	}
	got, err = s.GetMemoryV2(ctx, rec.ProjectID, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != rec.Body || got.Revision != current.Revision+1 || result.Generation != state.Generation+1 {
		t.Fatalf("nonmonotonic restore: %+v %+v", got, result)
	}
	if _, err = s.ApplyMemoryRestore(WithCommand(ctx, command), snap, state, []model.MemoryRecordV2{rec}); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("stale state accepted: %v", err)
	}
	var n int
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM command_audit WHERE operation='memory.restore'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("audit count %d: %v", n, err)
	}
}
