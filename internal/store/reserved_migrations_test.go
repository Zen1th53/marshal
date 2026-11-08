package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestL1MigrationsReserve92Through96(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if got := queryInt(t, s.db, "SELECT count(*) FROM schema_migrations WHERE version BETWEEN 92 AND 96"); got != 0 {
		t.Fatalf("reserved migration receipts = %d", got)
	}
	for version, table := range map[int]string{97: "operator_read_grants", 98: "memory_restore_snapshots", 99: "session_recovery_revisions"} {
		if got := queryInt(t, s.db, "SELECT count(*) FROM schema_migrations WHERE version = ?", version); got != 1 {
			t.Fatalf("migration %d receipts = %d", version, got)
		}
		if got := queryInt(t, s.db, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table); got != 1 {
			t.Fatalf("table %s missing", table)
		}
	}
	// Re-run the upgrade from v91 with compatible existing structures and data.
	// This also proves that the runner deliberately accepts a numeric gap.
	if err := s.SetOperatorReadGrant(ctx, "reserved-test", "/history", true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM schema_migrations WHERE version > 91"); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	grants, err := s.ListOperatorReadGrants(ctx, "reserved-test")
	if err != nil || len(grants) != 1 || !grants[0].Allowed {
		t.Fatalf("upgrade lost grant: %v %v", grants, err)
	}
	if got := queryInt(t, s.db, "SELECT max(version) FROM schema_migrations"); got != LatestSchemaVersion {
		t.Fatalf("upgrade version = %d, want %d", got, LatestSchemaVersion)
	}
}
