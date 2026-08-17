package store

import (
	"context"
	"testing"
)

func TestT22MigrationAllocatesVersionAfterExistingT05Schema(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if got := queryInt(t, st.db, "SELECT max(version) FROM schema_migrations"); got != 26 {
		t.Fatalf("schema version = %d, want 26 after T22", got)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='egress_decisions'"); got != 1 {
		t.Fatalf("egress_decisions tables = %d, want 1", got)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='verification_attestations'"); got != 1 {
		t.Fatalf("verification_attestations tables = %d, want 1", got)
	}
}
