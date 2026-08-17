package store

import (
	"context"
	"testing"
)

func TestT05A02MigrationCreatesVerificationAttestationStore(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='verification_attestations'"); got != 1 {
		t.Fatalf("verification_attestations table count=%d", got)
	}
	for _, index := range []string{"verification_attestations_by_change", "verification_attestations_by_principal"} {
		if got := queryInt(t, st.db, "SELECT count(*) FROM sqlite_master WHERE type='index' AND name=?", index); got != 1 {
			t.Fatalf("index %s count=%d", index, got)
		}
	}
}
