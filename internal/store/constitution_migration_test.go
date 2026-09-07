package store

import (
	"context"
	"testing"
)

var constitutionalTables = []string{
	"session_constitutions", "constitutional_decisions", "constitutional_violations",
}

// A clean install must reach the latest schema and create the constitutional
// tables. This is the install half of the migration contract.
func TestCleanInstallCreatesConstitutionalSchema(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if got := queryInt(t, st.db, "SELECT max(version) FROM schema_migrations"); got != LatestSchemaVersion {
		t.Fatalf("clean install reached schema %d, want %d", got, LatestSchemaVersion)
	}
	for _, table := range constitutionalTables {
		if got := queryInt(t, st.db,
			"SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table); got != 1 {
			t.Fatalf("clean install did not create %s", table)
		}
	}
}

// Migration must be idempotent: re-running it changes nothing and does not
// error, which is what makes a repeated or crashed startup safe.
func TestConstitutionMigrationIsIdempotent(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	for i := 0; i < 3; i++ {
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("re-running migration failed on pass %d: %v", i, err)
		}
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM schema_migrations WHERE version = 80"); got != 1 {
		t.Fatalf("migration 80 recorded %d times, want exactly 1", got)
	}
}

// An upgrade from the previous schema must add the constitutional tables while
// preserving every row already present: no silent canonical-state loss.
func TestUpgradeFromSchema79PreservesExistingState(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	// Rewind to the pre-80 shape with a row already present, then upgrade.
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO projects(project_id, repository, default_branch, pack_version, created_at)
		VALUES('PRJ-UPGRADE', '/tmp/repo', 'main', '6.0.0', '2026-01-01T00:00:00Z');
		DROP TABLE constitutional_violations;
		DROP TABLE constitutional_decisions;
		DROP TABLE session_constitutions;
		DELETE FROM schema_migrations WHERE version = 80;
	`); err != nil {
		t.Fatalf("rewind to schema 79: %v", err)
	}

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("upgrade from schema 79 failed: %v", err)
	}
	if got := queryInt(t, st.db, "SELECT max(version) FROM schema_migrations"); got != LatestSchemaVersion {
		t.Fatalf("upgrade reached schema %d, want %d", got, LatestSchemaVersion)
	}
	for _, table := range constitutionalTables {
		if got := queryInt(t, st.db,
			"SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table); got != 1 {
			t.Fatalf("upgrade did not create %s", table)
		}
	}

	// The row present before the upgrade must have survived untouched.
	var repository string
	if err := st.db.QueryRowContext(ctx,
		"SELECT repository FROM projects WHERE project_id = 'PRJ-UPGRADE'").Scan(&repository); err != nil {
		t.Fatalf("state present before the upgrade was lost: %v", err)
	}
	if repository != "/tmp/repo" {
		t.Fatalf("the upgrade altered a pre-existing row: repository = %q", repository)
	}
}

// The schema itself refuses states the constitution does not define, so a
// direct database write cannot introduce an outcome the gate can never produce.
func TestConstitutionalSchemaRejectsUndefinedStates(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO projects(project_id, repository, default_branch, pack_version, created_at)
		VALUES('PRJ-CHECK', '/tmp/r', 'main', '6.0.0', '2026-01-01T00:00:00Z')
	`); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	insertDecision := func(id, outcome, surface, mode, advisory string) error {
		_, err := st.db.ExecContext(ctx, `
			INSERT INTO constitutional_decisions(
				decision_id, project_id, session_id, constitution_version, process,
				domain, action, actor, surface, mode, outcome, reason,
				binding_digest, state_digest, advisory_status, evaluated_at)
			VALUES(?, 'PRJ-CHECK', 's1', '1.0.0', 5, 'file.mutation', 'write',
				'actor', ?, ?, ?, 'CONST_ALLOWED', 'sha256:b', 'sha256:s', ?, '2026-01-01T00:00:00Z')
		`, id, surface, mode, outcome, advisory)
		return err
	}

	if err := insertDecision("d-valid", "ALLOW", "tui", "standard", "ABSENT"); err != nil {
		t.Fatalf("a valid decision row was rejected: %v", err)
	}
	for name, args := range map[string][4]string{
		"invented outcome":        {"DEFINITELY_ALLOW", "tui", "standard", "ABSENT"},
		"invented surface":        {"ALLOW", "backdoor", "standard", "ABSENT"},
		"invented mode":           {"ALLOW", "tui", "superuser", "ABSENT"},
		"invented advisory state": {"ALLOW", "tui", "standard", "TRUSTED"},
	} {
		if err := insertDecision("d-"+name, args[0], args[1], args[2], args[3]); err == nil {
			t.Fatalf("the schema accepted an %s", name)
		}
	}

	// An out-of-range process number is refused.
	if err := insertDecision2(ctx, st, 99); err == nil {
		t.Fatal("the schema accepted a decision outside processes 1-8")
	}

	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO constitutional_violations(
			violation_id, project_id, session_id, violation_class, invariant_id,
			response, constitution_version, detected_at)
		VALUES('v1', 'PRJ-CHECK', 's1', 'SANDBOX_BYPASS', 'CI-005', 'IGNORE', '1.0.0', '2026-01-01T00:00:00Z')
	`); err == nil {
		t.Fatal("the schema accepted an undefined violation response")
	}

	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO session_constitutions(session_id, project_id, constitution_version, invariant_digest, mode, bound_at)
		VALUES('s-bad', 'PRJ-CHECK', '1.0.0', 'sha256:d', 'godmode', '2026-01-01T00:00:00Z')
	`); err == nil {
		t.Fatal("the schema accepted an undefined session mode")
	}
}

func insertDecision2(ctx context.Context, st *Store, process int) error {
	_, err := st.db.ExecContext(ctx, `
		INSERT INTO constitutional_decisions(
			decision_id, project_id, session_id, constitution_version, process,
			domain, action, actor, surface, mode, outcome, reason,
			binding_digest, state_digest, advisory_status, evaluated_at)
		VALUES('d-process', 'PRJ-CHECK', 's1', '1.0.0', ?, 'file.mutation', 'write',
			'actor', 'tui', 'standard', 'ALLOW', 'CONST_ALLOWED', 'sha256:b', 'sha256:s',
			'ABSENT', '2026-01-01T00:00:00Z')
	`, process)
	return err
}
