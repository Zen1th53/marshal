package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/secrets"
)

func TestSecretLeaseStaleClaimCanBeReclaimedByCurrentOwner(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "secret.db")
	st, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `INSERT INTO projects(project_id, repository, default_branch, pack_version, created_at) VALUES('project-recovery', '/repo', 'main', '1', '2026-01-01T00:00:00Z'); INSERT INTO tasks(task_id, project_id, title, status, risk, revision, created_at, updated_at) VALUES('task-recovery', 'project-recovery', 'secret', 'proposed', 'R1', 0, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	engine, err := secrets.NewEngine(secrets.EngineConfig{Store: st, Capability: allowingSecretCapability{}, Providers: map[string]secrets.Provider{"env": countingSecretProvider{calls: &calls}}, Now: func() time.Time { return time.Unix(100, 0).UTC() }})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := engine.Lease(ctx, secrets.LeaseRequest{ID: "lease-recovery", Subject: "agent", TaskID: "task-recovery", Ref: secrets.Ref{Provider: "env", Name: "TOKEN", Version: "v1"}, Purpose: "test", IssuedAt: time.Unix(100, 0).UTC(), ExpiresAt: time.Unix(200, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimSecretLease(ctx, lease.ID, "stale-owner", time.Unix(1, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	if err := engine.WithSecret(ctx, lease, func([]byte) error { return nil }); err != nil {
		t.Fatalf("reclaim error=%v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("provider calls=%d, want 1", calls.Load())
	}
	if _, err := st.CompleteSecretLease(ctx, lease.ID, "stale-owner", time.Unix(100, 0).UTC()); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("stale owner completion error=%v, want conflict", err)
	}
}
