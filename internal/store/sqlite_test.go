package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestMigrateCreatesCanonicalSchemaWithForeignKeys(t *testing.T) {
	// Deliberately the empty store: this is the test that drives the whole
	// migration chain from nothing and checks what it produced, so it must not
	// receive the template that chain already built.
	st := openEmptyTestStore(t)
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
		"structured_events",
		"dag_nodes", "dag_edges",
		"capability_grants",
		"execution_cells",
		"role_bindings",
		"gate_decisions",
		"secret_leases",
		"risk_assessments",
		"egress_decisions",
		"trusted_content_segments",
		"typed_handoffs",
		"mcp_gateway_logs",
	}
	for _, table := range wantTables {
		if got := queryInt(t, st.db, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table); got != 1 {
			t.Errorf("table %s count = %d, want 1", table, got)
		}
	}
}

func TestRetiredSchemaVersionsStayInLedgerWithoutTables(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM schema_migrations WHERE version BETWEEN 27 AND 67"); got != 41 {
		t.Fatalf("ledger versions 27..67 = %d, want 41", got)
	}
	for _, table := range []string{"verification_attestations", "persistent_agent_memory", "merge_queue_entries"} {
		if got := queryInt(t, st.db, "SELECT count(*) FROM sqlite_master WHERE name=?", table); got != 0 {
			t.Fatalf("retired table %s created on a new database", table)
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
		"DROP INDEX IF EXISTS mcp_gateway_logs_by_task",
		"DROP TABLE IF EXISTS mcp_gateway_logs",
		"DROP INDEX typed_handoffs_by_sender",
		"DROP INDEX typed_handoffs_by_task_status",
		"DROP TABLE typed_handoffs",
		"DROP INDEX trusted_content_segments_by_source",
		"DROP INDEX trusted_content_segments_by_state",
		"DROP TABLE trusted_content_segments",
		"DROP INDEX egress_decisions_by_created",
		"DROP TABLE egress_decisions",
		"DROP INDEX risk_assessments_by_action",
		"DROP INDEX risk_assessments_by_created",
		"DROP TABLE risk_assessments",
		"DROP INDEX secret_leases_by_task_scope",
		"DROP INDEX secret_leases_by_expiry",
		"DROP TABLE secret_leases",
		"DROP INDEX gate_decisions_by_point_subject",
		"DROP INDEX gate_decisions_by_resource_change",
		"DROP TABLE gate_decisions",
		"DROP INDEX role_bindings_by_principal_scope",
		"DROP INDEX role_bindings_by_role",
		"DROP TABLE role_bindings",
		"DROP INDEX execution_cells_by_task",
		"DROP INDEX execution_cells_by_state",
		"DROP TABLE execution_cells",
		"DROP INDEX capability_grants_by_expiry",
		"DROP INDEX capability_grants_by_subject_task_kind",
		"DROP INDEX capability_grants_by_idempotency",
		"ALTER TABLE capability_grants DROP COLUMN idempotency_key",
		"DROP TABLE capability_grants",
		"DROP INDEX dag_edges_by_to",
		"DROP INDEX dag_edges_by_from",
		"DROP TABLE dag_edges",
		"DROP INDEX dag_nodes_by_status_priority",
		"DROP TABLE dag_nodes",
		"DROP INDEX structured_events_by_type_sequence",
		"DROP INDEX structured_events_by_run_sequence",
		"DROP INDEX structured_events_by_task_sequence",
		"DROP INDEX structured_events_by_sequence",
		"DROP TABLE structured_events",
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
	resetProjectContext95HistoricalFixture(t, st)
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
		"DROP INDEX IF EXISTS mcp_gateway_logs_by_task",
		"DROP TABLE IF EXISTS mcp_gateway_logs",
		"DROP INDEX typed_handoffs_by_sender",
		"DROP INDEX typed_handoffs_by_task_status",
		"DROP TABLE typed_handoffs",
		"DROP INDEX trusted_content_segments_by_source",
		"DROP INDEX trusted_content_segments_by_state",
		"DROP TABLE trusted_content_segments",
		"DROP INDEX egress_decisions_by_created",
		"DROP TABLE egress_decisions",
		"DROP INDEX risk_assessments_by_action",
		"DROP INDEX risk_assessments_by_created",
		"DROP TABLE risk_assessments",
		"DROP INDEX secret_leases_by_task_scope",
		"DROP INDEX secret_leases_by_expiry",
		"DROP TABLE secret_leases",
		"DROP INDEX gate_decisions_by_point_subject",
		"DROP INDEX gate_decisions_by_resource_change",
		"DROP TABLE gate_decisions",
		"DROP INDEX role_bindings_by_principal_scope",
		"DROP INDEX role_bindings_by_role",
		"DROP TABLE role_bindings",
		"DROP INDEX execution_cells_by_task",
		"DROP INDEX execution_cells_by_state",
		"DROP TABLE execution_cells",
		"DROP INDEX capability_grants_by_expiry",
		"DROP INDEX capability_grants_by_subject_task_kind",
		"DROP INDEX capability_grants_by_idempotency",
		"ALTER TABLE capability_grants DROP COLUMN idempotency_key",
		"DROP TABLE capability_grants",
		"DROP INDEX dag_edges_by_to",
		"DROP INDEX dag_edges_by_from",
		"DROP TABLE dag_edges",
		"DROP INDEX dag_nodes_by_status_priority",
		"DROP TABLE dag_nodes",
		"DROP INDEX structured_events_by_type_sequence",
		"DROP INDEX structured_events_by_run_sequence",
		"DROP INDEX structured_events_by_task_sequence",
		"DROP INDEX structured_events_by_sequence",
		"DROP TABLE structured_events",
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
	resetProjectContext95HistoricalFixture(t, st)
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

func TestInitProjectIsIdempotentUpdatesMetadataAndRejectsConflictingIdentity(t *testing.T) {
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

	// Branch and pack version reflect the current checkout/runtime rather than
	// the repository's identity. An explicit re-init must refresh them without
	// discarding the durable project state.
	project.DefaultBranch = "release/current"
	project.PackVersion = "6.1.0"
	if err := st.InitProject(ctx, project); err != nil {
		t.Fatalf("metadata refresh: %v", err)
	}
	var branch, pack string
	if err := st.db.QueryRowContext(ctx, `SELECT default_branch, pack_version FROM projects WHERE project_id = ?`, project.ID).Scan(&branch, &pack); err != nil {
		t.Fatal(err)
	}
	if branch != project.DefaultBranch || pack != project.PackVersion {
		t.Fatalf("metadata = branch %q pack %q, want branch %q pack %q", branch, pack, project.DefaultBranch, project.PackVersion)
	}

	project.Repository = "/other"
	if err := st.InitProject(ctx, project); err == nil {
		t.Fatal("conflicting project identity was accepted")
	}
}

// openEmptyTestStore opens a store on an empty database, with no schema.
//
// Use it when the test is about the migration chain itself: one that drives it
// from nothing, or one that seeds a historical schema version and migrates
// forward from there. Everything else wants openTestStore.
func openEmptyTestStore(t *testing.T) *Store {
	t.Helper()
	return openStoreFile(t, filepath.Join(t.TempDir(), "state.db"))
}

// openTestStore opens a store already migrated to the current schema.
//
// The chain costs about a second per call under the race detector, and the
// suite opened a store three hundred times, which was most of the package's
// runtime. It runs once now, into a template that each test receives its own
// copy of.
//
// This is a copy and not a shared handle: a test that writes to its store must
// not be visible to the next one. Tests that call Migrate afterwards still
// work and still cost nothing — Migrate reads the ledger, finds the schema
// already current, and returns.
func openTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	if err := os.WriteFile(path, migratedTemplate(t), 0o600); err != nil {
		t.Fatalf("seed store from template: %v", err)
	}
	return openStoreFile(t, path)
}

// openStoreFile opens a store and reports a failing Close, which the shared
// openStoreAt in the concurrency tests deliberately ignores.
func openStoreFile(t *testing.T, path string) *Store {
	t.Helper()
	st, err := Open(context.Background(), path)
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

var (
	templateOnce  sync.Once
	templateBytes []byte
	templateErr   error
)

// migratedTemplate returns the bytes of a database at the current schema,
// built once per test binary by the real migration chain.
//
// It is built by Migrate rather than by a checked-in fixture on purpose: a
// fixture would drift from the chain silently, and the point of the template
// is to be what the chain produces. The schema version is asserted before the
// bytes are handed out, so a chain that stops short fails every test that asks
// for a store rather than leaving them to fail obscurely one by one.
func migratedTemplate(t *testing.T) []byte {
	t.Helper()
	templateOnce.Do(func() {
		dir, err := os.MkdirTemp("", "marshal-store-template-")
		if err != nil {
			templateErr = fmt.Errorf("template dir: %w", err)
			return
		}
		defer os.RemoveAll(dir)

		path := filepath.Join(dir, "template.db")
		ctx := context.Background()
		st, err := Open(ctx, path)
		if err != nil {
			templateErr = fmt.Errorf("open template: %w", err)
			return
		}
		if err := st.Migrate(ctx); err != nil {
			st.Close()
			templateErr = fmt.Errorf("migrate template: %w", err)
			return
		}
		var version int
		if err := st.db.QueryRowContext(ctx,
			"SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&version); err != nil {
			st.Close()
			templateErr = fmt.Errorf("read template schema version: %w", err)
			return
		}
		if version != LatestSchemaVersion {
			st.Close()
			templateErr = fmt.Errorf("template schema version = %d, want %d", version, LatestSchemaVersion)
			return
		}
		// Fold the write-ahead log back into the main file, so the copy each
		// test receives is complete on its own and carries no -wal sidecar.
		if _, err := st.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			st.Close()
			templateErr = fmt.Errorf("checkpoint template: %w", err)
			return
		}
		if err := st.Close(); err != nil {
			templateErr = fmt.Errorf("close template: %w", err)
			return
		}
		templateBytes, err = os.ReadFile(path)
		if err != nil {
			templateErr = fmt.Errorf("read template: %w", err)
		}
	})
	if templateErr != nil {
		t.Fatalf("build migrated template: %v", templateErr)
	}
	return templateBytes
}

func queryInt(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var value int
	if err := db.QueryRowContext(context.Background(), query, args...).Scan(&value); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return value
}
