package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
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
	if got := queryInt(t, st.db, "SELECT max(version) FROM schema_migrations"); got != LatestSchemaVersion {
		t.Fatalf("schema version = %d, want latest", got)
	}

	wantTables := []string{
		"projects", "agents", "sessions", "tasks", "task_dependencies",
		"leases", "decisions", "findings", "handoffs", "checkpoints",
		"approvals", "artifacts", "audit_events", "memory_records",
		"worker_runs", "verifications",
		"evidence_nodes", "evidence_edges",
		"policy_versions",
		"policy_test_runs", "policy_test_cases", "policy_test_outcomes",
		"dag_nodes", "dag_edges",
	}
	for _, table := range wantTables {
		if got := queryInt(t, st.db, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table); got != 1 {
			t.Errorf("table %s count = %d, want 1", table, got)
		}
	}
}

func TestPolicyTestRecoveryMigrationsFromV9(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"DROP INDEX dag_edges_by_to",
		"DROP INDEX dag_edges_by_from",
		"DROP TABLE dag_edges",
		"DROP INDEX dag_nodes_by_status_priority",
		"DROP TABLE dag_nodes",
		"DROP INDEX policy_test_outcomes_by_status",
		"DROP TABLE policy_test_outcomes",
		"ALTER TABLE policy_test_runs DROP COLUMN execution_claimed_at",
		"ALTER TABLE policy_test_runs DROP COLUMN execution_owner",
		"DELETE FROM schema_migrations WHERE version >= 10",
	} {
		if _, err := st.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("prepare v9 schema with %q: %v", statement, err)
		}
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate v9: %v", err)
	}
	if got := queryInt(t, st.db, "SELECT max(version) FROM schema_migrations"); got != LatestSchemaVersion {
		t.Fatalf("schema version=%d, want latest", got)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM pragma_table_info('policy_test_runs') WHERE name IN ('execution_owner','execution_claimed_at')"); got != 2 {
		t.Fatalf("claim columns=%d, want 2", got)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='policy_test_outcomes'"); got != 1 {
		t.Fatalf("outcomes table=%d, want 1", got)
	}
	var integrity string
	if err := st.db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity=%q err=%v", integrity, err)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM pragma_foreign_key_check"); got != 0 {
		t.Fatalf("foreign key violations=%d", got)
	}
}

func TestPolicyTestRecoveryMigrationsFromPreT49V7(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"DROP INDEX dag_edges_by_to",
		"DROP INDEX dag_edges_by_from",
		"DROP TABLE dag_edges",
		"DROP INDEX dag_nodes_by_status_priority",
		"DROP TABLE dag_nodes",
		"DROP INDEX policy_test_outcomes_by_status",
		"DROP TABLE policy_test_outcomes",
		"ALTER TABLE policy_test_runs DROP COLUMN execution_claimed_at",
		"ALTER TABLE policy_test_runs DROP COLUMN execution_owner",
		"ALTER TABLE policy_test_runs DROP COLUMN state",
		"DROP INDEX policy_test_cases_by_status",
		"DROP TABLE policy_test_cases",
		"DROP INDEX policy_test_runs_by_policy",
		"DROP TABLE policy_test_runs",
		"DELETE FROM schema_migrations WHERE version >= 8",
	} {
		if _, err := st.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("prepare v7 schema with %q: %v", statement, err)
		}
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate v7 to latest: %v", err)
	}
	if got := queryInt(t, st.db, "SELECT max(version) FROM schema_migrations"); got != LatestSchemaVersion {
		t.Fatalf("schema version=%d, want latest", got)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('policy_test_runs','policy_test_cases','policy_test_outcomes')"); got != 3 {
		t.Fatalf("T49 tables=%d, want 3", got)
	}
	var integrity string
	if err := st.db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity=%q err=%v", integrity, err)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM pragma_foreign_key_check"); got != 0 {
		t.Fatalf("foreign key violations=%d", got)
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
