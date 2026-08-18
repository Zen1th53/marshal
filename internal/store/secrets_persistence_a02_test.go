package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/secrets"
)

func TestSecretLeasePersistenceSurvivesReopenWithoutSecretValue(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "secret.db")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := first.db.ExecContext(ctx, `INSERT INTO projects(project_id, repository, default_branch, pack_version, created_at) VALUES('project-a02', '/repo', 'main', '1', '2026-01-01T00:00:00Z'); INSERT INTO tasks(task_id, project_id, title, status, risk, revision, created_at, updated_at) VALUES('task-a02', 'project-a02', 'secret lease', 'proposed', 'R1', 0, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if version, err := first.SchemaVersion(ctx); err != nil || version != LatestSchemaVersion {
		t.Fatalf("schema version=%d err=%v, want %d", version, err, LatestSchemaVersion)
	}
	lease := secrets.Lease{
		ID: "lease-a02", Subject: "agent-a02", TaskID: "task-a02",
		Ref:     secrets.Ref{Provider: "env", Name: "API_TOKEN", Version: "v1"},
		Purpose: "deploy", IssuedAt: time.Unix(10, 0).UTC(), ExpiresAt: time.Unix(20, 0).UTC(), State: secrets.StateRequested,
	}
	if err := first.PutSecretLease(ctx, lease); err != nil {
		t.Fatalf("PutSecretLease: %v", err)
	}
	got, err := first.GetSecretLease(ctx, lease.ID)
	if err != nil {
		t.Fatalf("GetSecretLease: %v", err)
	}
	if got != lease {
		t.Fatalf("lease = %#v, want %#v", got, lease)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	got, err = second.GetSecretLease(ctx, lease.ID)
	if err != nil || got != lease {
		t.Fatalf("reopened lease=%#v err=%v", got, err)
	}
	var database []byte
	if err := second.db.QueryRowContext(ctx, "SELECT group_concat(sql, '\n') FROM sqlite_master WHERE sql IS NOT NULL").Scan(&database); err != nil {
		t.Fatal(err)
	}
	if string(database) == "MARSHAL_TEST_SECRET_T21_A02" {
		t.Fatal("secret marker persisted in schema metadata")
	}
}

func TestSecretLeasePersistenceRejectsInvalidAndDuplicateIdentity(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "secret.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	lease := secrets.Lease{ID: "lease-a02-invalid", Subject: "agent", TaskID: "task", Ref: secrets.Ref{Provider: "env", Name: "TOKEN", Version: "v1"}, Purpose: "test", IssuedAt: time.Unix(10, 0).UTC(), ExpiresAt: time.Unix(20, 0).UTC()}
	err = st.PutSecretLease(ctx, lease)
	if err == nil {
		t.Fatal("invalid foreign task accepted")
	}
	if !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("invalid task error=%v, want ErrInvalid", err)
	}
}
