package store

import (
	"context"
	"testing"
)

// goalIntakeColumns are the fields Process 03 added to the Goal contract.
var goalIntakeColumns = []string{
	"original_request", "project_id", "constitution_version",
	"confirmation_state", "assessment_json", "request_digest",
	"revision_reason", "advisory_used",
}

func TestCleanInstallCreatesGoalIntakeColumns(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	columns, err := tableColumnsForTest(ctx, st, "goal_contracts")
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range goalIntakeColumns {
		if !columns[column] {
			t.Fatalf("a clean install did not create goal_contracts.%s", column)
		}
	}
}

// The migration must tolerate being re-run. SQLite has no ADD COLUMN IF NOT
// EXISTS, and the repository's upgrade tests rewind the ledger without
// dropping columns, so a replay must find the columns already present rather
// than failing.
func TestGoalIntakeMigrationIsReplaySafe(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	// Rewind the ledger while leaving the columns in place, which is exactly
	// the shape a partially applied or replayed migration takes.
	if _, err := st.db.ExecContext(ctx,
		"DELETE FROM schema_migrations WHERE version >= 81"); err != nil {
		t.Fatalf("rewind the ledger: %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("replaying the migration over existing columns failed: %v", err)
	}
	if got := queryInt(t, st.db, "SELECT max(version) FROM schema_migrations"); got != LatestSchemaVersion {
		t.Fatalf("replay reached schema %d, want %d", got, LatestSchemaVersion)
	}

	// Running it again from a settled state is likewise a no-op.
	for i := 0; i < 3; i++ {
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("re-running migration failed on pass %d: %v", i, err)
		}
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM schema_migrations WHERE version = 81"); got != 1 {
		t.Fatalf("migration 81 recorded %d times, want exactly 1", got)
	}
}

// A Goal written before this migration keeps working and simply has no
// original request recorded, which is honest. Losing such rows would be worse
// than admitting what was never captured.
func TestExistingGoalsSurviveTheUpgrade(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO goal_contracts(
			goal_id, session_id, revision, desired_outcome, expected_artifact,
			risk, authority_source, understanding_state, created_at, updated_at)
		VALUES('GOAL-legacy', 'SESSION-1', 1, 'an earlier goal', 'a result',
			'R1', 'operator', 'READY', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')
	`); err != nil {
		t.Fatalf("insert a pre-migration goal: %v", err)
	}

	var outcome, original, confirmation string
	if err := st.db.QueryRowContext(ctx, `
		SELECT desired_outcome, original_request, confirmation_state
		FROM goal_contracts WHERE goal_id = 'GOAL-legacy'
	`).Scan(&outcome, &original, &confirmation); err != nil {
		t.Fatalf("a pre-migration goal became unreadable: %v", err)
	}
	if outcome != "an earlier goal" {
		t.Fatalf("the upgrade altered an existing goal: %q", outcome)
	}
	if original != "" {
		t.Fatalf("a goal with no recorded request reported one: %q", original)
	}
	if confirmation != "PENDING" {
		t.Fatalf("a pre-migration goal defaulted to confirmation %q; it must not read as approved", confirmation)
	}
}

// The schema refuses confirmation states the intake model cannot produce, so a
// direct write cannot mark a Goal approved in a way the code never would.
func TestGoalConfirmationStateIsConstrained(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	insert := func(id, state string) error {
		_, err := st.db.ExecContext(ctx, `
			INSERT INTO goal_contracts(
				goal_id, session_id, revision, desired_outcome, expected_artifact,
				risk, authority_source, understanding_state, confirmation_state,
				created_at, updated_at)
			VALUES(?, 'SESSION-1', 1, 'outcome', 'artifact', 'R1', 'operator', 'READY', ?,
				'2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')
		`, id, state)
		return err
	}
	for _, state := range []string{"PENDING", "APPROVED", "DELEGATED", "CANCELLED", "NEEDS_INPUT"} {
		if err := insert("GOAL-"+state, state); err != nil {
			t.Fatalf("a defined confirmation state was rejected: %s: %v", state, err)
		}
	}
	for _, state := range []string{"AUTO_APPROVED", "TRUSTED", "SKIPPED", ""} {
		if err := insert("GOAL-bad-"+state, state); err == nil {
			t.Fatalf("the schema accepted an undefined confirmation state: %q", state)
		}
	}
}

func tableColumnsForTest(ctx context.Context, st *Store, table string) (map[string]bool, error) {
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return tableColumns(ctx, tx, table)
}
