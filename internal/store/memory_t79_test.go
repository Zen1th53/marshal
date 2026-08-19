package store

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// TestT79CanonicalMemoryV2TableExists verifies that after migration v68 the
// memory_records_v2 table is present in the schema.
func TestT79CanonicalMemoryV2TableExists(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	var n int
	if err := st.db.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='memory_records_v2'`,
	).Scan(&n); err != nil || n != 1 {
		t.Fatalf("memory_records_v2 table not found: err=%v n=%d", err, n)
	}
}

// TestT79WriteAndReadMemoryRecordV2 verifies end-to-end write/read of a
// canonical v2 record.
func TestT79WriteAndReadMemoryRecordV2(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := st.InitProject(ctx, model.Project{
		ID: "PROJ-T79", Repository: "r", DefaultBranch: "main", PackVersion: "1.0.0",
	}); err != nil {
		t.Fatalf("InitProject: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	rec := model.MemoryRecordV2{
		ID:         "MEM-T79-001",
		ProjectID:  "PROJ-T79",
		Kind:       model.MemoryKindDecision,
		Lifecycle:  model.MemoryDurable,
		Authority:  model.AuthorityVerified,
		Title:      "Use SQLite canonical store",
		Body:       "Decision rationale: single-host SQLite is sufficient for v1",
		Scope:      "project",
		ScopeID:    "PROJ-T79",
		ObservedAt: now.Add(-time.Hour),
		IngestedAt: now,
		ValidFrom:  now.Add(-time.Hour),
		CreatedAt:  now,
		UpdatedAt:  now,
		Source: model.MemorySource{
			Kind:      "runtime",
			Reference: "TASK-001",
		},
	}

	if err := st.WriteMemoryV2(ctx, rec); err != nil {
		t.Fatalf("WriteMemoryV2: %v", err)
	}

	got, err := st.GetMemoryV2(ctx, "PROJ-T79", "MEM-T79-001")
	if err != nil {
		t.Fatalf("GetMemoryV2: %v", err)
	}
	if got.ID != rec.ID {
		t.Errorf("ID: got %q want %q", got.ID, rec.ID)
	}
	if got.Kind != rec.Kind {
		t.Errorf("Kind: got %q want %q", got.Kind, rec.Kind)
	}
	if got.Lifecycle != rec.Lifecycle {
		t.Errorf("Lifecycle: got %q want %q", got.Lifecycle, rec.Lifecycle)
	}
	if got.Body != rec.Body {
		t.Errorf("Body: got %q want %q", got.Body, rec.Body)
	}
	if got.ContentDigest == "" {
		t.Error("expected ContentDigest to be set by store on write")
	}
	if got.ContentDigest != rec.CanonicalDigest() {
		t.Errorf("ContentDigest mismatch: got %s want %s", got.ContentDigest, rec.CanonicalDigest())
	}
}

// TestT79MigrationIdempotent verifies that opening the same database twice
// and calling Migrate both times succeeds without error.
func TestT79MigrationIdempotent(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	open := func() *Store {
		st, err := Open(ctx, dir+"/state.db")
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		t.Cleanup(func() { st.Close() })
		return st
	}

	st1 := open()
	if err := st1.Migrate(ctx); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}

	st2 := open()
	if err := st2.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate (idempotent): %v", err)
	}
}

// TestT79LegacyTablesPreservedAfterMigration verifies that the four legacy
// memory tables still exist after v68 migration.
func TestT79LegacyTablesPreservedAfterMigration(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	for _, table := range []string{
		"memory_records",
		"persistent_agent_memory",
		"decision_records",
		"failure_memory_records",
	} {
		var n int
		if err := st.db.QueryRowContext(ctx,
			`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`,
			table).Scan(&n); err != nil || n != 1 {
			t.Errorf("legacy table %q should be preserved: err=%v n=%d", table, err, n)
		}
	}
}

// TestT79WriteRejectsInvalidRecord verifies that WriteMemoryV2 rejects
// records that fail validation at the store boundary.
func TestT79WriteRejectsInvalidRecord(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	bad := model.MemoryRecordV2{
		ID: "", // missing required field
	}
	if err := st.WriteMemoryV2(ctx, bad); err == nil {
		t.Error("expected error for invalid record with empty ID")
	}
}
