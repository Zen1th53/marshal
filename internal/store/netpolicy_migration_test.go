package store

import (
	"context"
	"testing"
)

func TestT22MigrationAllocatesVersionAfterExistingSchema(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if got := queryInt(t, st.db, "SELECT max(version) FROM schema_migrations"); got != LatestSchemaVersion {
		t.Fatalf("schema version = %d, want %d after T22", got, LatestSchemaVersion)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='egress_decisions'"); got != 1 {
		t.Fatalf("egress_decisions tables = %d, want 1", got)
	}
}
