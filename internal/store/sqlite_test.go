package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/slaves/internal/model"
)

func TestMigrateCreatesCanonicalSchemaWithForeignKeys(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}

	if got := queryInt(t, st.db, "PRAGMA foreign_keys"); got != 1 {
		t.Fatalf("foreign_keys = %d, want 1", got)
	}
	if got := queryInt(t, st.db, "SELECT max(version) FROM schema_migrations"); got != 1 {
		t.Fatalf("schema version = %d, want 1", got)
	}

	wantTables := []string{
		"projects", "agents", "sessions", "tasks", "task_dependencies",
		"leases", "decisions", "findings", "handoffs", "checkpoints",
		"approvals", "artifacts", "audit_events", "memory_records",
		"worker_runs", "verifications",
	}
	for _, table := range wantTables {
		if got := queryInt(t, st.db, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table); got != 1 {
			t.Errorf("table %s count = %d, want 1", table, got)
		}
	}
}

func TestInitProjectIsIdempotentAndRejectsConflictingIdentity(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	project := model.Project{
		ID:            "PROJECT-local",
		Repository:    "/repo",
		DefaultBranch: "main",
		PackVersion:   "6.0.0",
	}
	if err := st.InitProject(ctx, project); err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	if err := st.InitProject(ctx, project); err != nil {
		t.Fatalf("second InitProject: %v", err)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM projects"); got != 1 {
		t.Fatalf("project rows = %d, want 1", got)
	}

	project.Repository = "/other"
	if err := st.InitProject(ctx, project); err == nil {
		t.Fatal("conflicting project identity was accepted")
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return st
}

func queryInt(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var value int
	if err := db.QueryRowContext(context.Background(), query, args...).Scan(&value); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return value
}
