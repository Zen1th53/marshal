package app

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

func memoryOperatorRuntime(t *testing.T) (*Runtime, context.Context) {
	t.Helper()
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	r, err := Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	local, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ctx = local.Context(ctx)
	p, _ := auth.LocalFromContext(ctx)
	// The integrator owns production grant registration. These explicitly
	// provision isolated fixture grants without changing OpenLocalControl.
	for _, op := range []string{"memory.snapshot", "memory.restore", "memory.promote", "memory.slot-promote", "memory.import", "memory.export", "verification.challenge", "verification.close-finding", "verification.attest", "verification.build-bundle", "verification.export-bundle"} {
		grant := capability.Grant{ID: capability.GrantID("test-" + op), Subject: capability.SubjectID(p.ID()), TaskID: capability.TaskID(r.ProjectIdentity()), Kind: capability.KindFilesystemWrite, Scope: capability.Scope{Resource: r.layout.Database, Actions: []string{op}}, IssuedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour), Issuer: capability.SubjectID(p.ID()), IdempotencyKey: "test-" + op}
		if err = r.store.PutCapabilityGrant(ctx, grant); err != nil {
			t.Fatal(err)
		}
	}
	// Provisional v92 service fixture, never clean-install qualification.
	db, err := sql.Open("sqlite", r.layout.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var exists int
	if err = db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='memory_restore_snapshots'`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists == 0 {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = store.MigrateMemoryRestore(ctx, tx); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	return r, ctx
}
func seedRestoreMemory(t *testing.T, r *Runtime, ctx context.Context) model.MemoryRecordV2 {
	t.Helper()
	p, err := r.memoryReviewPrincipal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := r.memoryService.Remember(ctx, p, RememberRequest{ProjectID: r.ProjectID(), Title: "before restore", Body: "historical fact", ScopeID: r.ProjectID(), Kind: model.MemoryKindSemantic})
	if err != nil {
		t.Fatal(err)
	}
	rec, err = r.store.GetMemoryV2(ctx, rec.ProjectID, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}
func snapshotAndReview(t *testing.T, r *Runtime, ctx context.Context, rec model.MemoryRecordV2) (MemoryRestoreReview, CommandEnvelope, MemoryRestoreRequest) {
	t.Helper()
	snap, err := r.CreateMemorySnapshot(ctx, CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "session", TargetID: "snapshot", IdempotencyKey: "snapshot-once"}, "before", []string{rec.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.store.UpdateMemory(ctx, rec.ProjectID, rec.ID, rec.Revision, func(m *model.MemoryRecordV2) error { m.Body = "later fact"; return nil }); err != nil {
		t.Fatal(err)
	}
	review, err := r.ReviewMemoryRestore(ctx, snap.ID)
	if err != nil {
		t.Fatal(err)
	}
	e := CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "session", TargetID: snap.ID, ExpectedVersion: review.Generation, IdempotencyKey: "restore-once"}
	req := MemoryRestoreRequest{SnapshotID: snap.ID, SnapshotDigest: snap.Digest, StateDigest: review.StateDigest, PolicyDigest: review.PolicyDigest, Reason: "restore reviewed historical fact"}
	return review, e, req
}

func TestMemoryRestoreRestartReplay(t *testing.T) {
	r, ctx := memoryOperatorRuntime(t)
	rec := seedRestoreMemory(t, r, ctx)
	_, e, req := snapshotAndReview(t, r, ctx, rec)
	first, err := r.CommandRestoreMemory(ctx, e, req)
	if err != nil {
		t.Fatal(err)
	}
	key, err := os.Stat(filepath.Join(r.layout.RuntimeDir, "memory-restore.key"))
	if err != nil || key.Mode().Perm() != 0600 {
		t.Fatalf("key protections: %v", err)
	}
	root := r.layout.Root
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	local, err := reopened.OpenLocalControl(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	replay, err := reopened.CommandRestoreMemory(local.Context(context.Background()), e, req)
	if err != nil {
		t.Fatal(err)
	}
	if replay.Generation != first.Generation {
		t.Fatalf("replay mutated generation: %+v %+v", first, replay)
	}
	got, err := reopened.store.GetMemoryV2(ctx, rec.ProjectID, rec.ID)
	if err != nil || got.Body != rec.Body || got.Revision != rec.Revision+2 {
		t.Fatalf("restart readback: %+v %v", got, err)
	}
	if err = revokeAllActionGrants(ctx, reopened.store, "memory.restore"); err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.CommandRestoreMemory(local.Context(context.Background()), e, req); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("revoked replay accepted: %v", err)
	}
}
func TestMemoryRestoreRevalidationCannotResurrectRevokedRecords(t *testing.T) {
	for _, mutation := range []string{"tombstone", "purge", "private", "writer"} {
		t.Run(mutation, func(t *testing.T) {
			r, ctx := memoryOperatorRuntime(t)
			rec := seedRestoreMemory(t, r, ctx)
			_, e, req := snapshotAndReview(t, r, ctx, rec)
			live, err := r.store.GetMemoryV2(ctx, rec.ProjectID, rec.ID)
			if err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "tombstone":
				_, err = r.store.TombstoneMemory(ctx, rec.ProjectID, rec.ID, live.Revision, "revoked")
			case "purge":
				db, openErr := sql.Open("sqlite", r.layout.Database)
				if openErr != nil {
					t.Fatal(openErr)
				}
				_, err = db.Exec(`DELETE FROM memory_records_v2 WHERE memory_id=?`, rec.ID)
				db.Close()
			case "private":
				_, err = r.store.UpdateMemory(ctx, rec.ProjectID, rec.ID, live.Revision, func(m *model.MemoryRecordV2) error { m.ACLScope = "other-operator"; return nil })
			case "writer":
				_, err = r.store.UpdateMemory(ctx, rec.ProjectID, rec.ID, live.Revision, func(m *model.MemoryRecordV2) error { m.Body = "concurrent writer"; return nil })
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = r.CommandRestoreMemory(ctx, e, req); err == nil {
				t.Fatalf("%s resurrected/replaced record", mutation)
			}
		})
	}
}
func TestMemoryRestoreProjectionRecovery(t *testing.T) {
	r, ctx := memoryOperatorRuntime(t)
	rec := seedRestoreMemory(t, r, ctx)
	_, e, req := snapshotAndReview(t, r, ctx, rec)
	after, err := r.CommandRestoreMemory(ctx, e, req)
	if err != nil {
		t.Fatal(err)
	}
	if after.ProjectionStatus != "REBUILT: canonical records reloaded" {
		t.Fatal(after.ProjectionStatus)
	}
	if err = r.memoryService.RebuildProjections(ctx, r.ProjectID()); err != nil {
		t.Fatal(err)
	}
	got, err := r.store.MemoryRestoreResult(ctx, r.ProjectID(), after.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProjectionStatus != "PENDING: durable outbox requires projection recovery" {
		t.Fatalf("durable record invents projection recovery: %s", got.ProjectionStatus)
	}
	events, err := r.store.FetchUnprocessedMemoryOutbox(ctx, r.ProjectID(), 128)
	if err != nil || len(events) < 3 {
		t.Fatalf("recovery outbox missing: %d %v", len(events), err)
	}
}
