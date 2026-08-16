package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestCapabilityGrantPersistenceMigrationCreatesCanonicalTable(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "capability.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	var table string
	if err := st.db.QueryRowContext(ctx, `
		SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'capability_grants'
	`).Scan(&table); err != nil {
		t.Fatalf("capability grant table missing: %v", err)
	}
	if table != "capability_grants" {
		t.Fatalf("table = %q", table)
	}

	var version int
	if err := st.db.QueryRowContext(ctx, "SELECT max(version) FROM schema_migrations").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 13 {
		t.Fatalf("schema version = %d, want 13", version)
	}
}
