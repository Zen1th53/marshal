package store

import (
	"context"
	"path/filepath"
	"testing"
)

// BenchmarkMigrationChain measures the cost of replaying every migration on a
// fresh database.
//
// This is not a micro-optimisation exercise. The chain is replayed by every
// store test — around 150 of them — so a few milliseconds added here is
// multiplied into minutes of wall time under the race detector, and the store
// package sits close to the 10-minute per-package timeout. A migration that
// looked free in isolation has caused a timeout before, which is why any new
// migration is measured here rather than assumed cheap.
func BenchmarkMigrationChain(b *testing.B) {
	ctx := context.Background()
	for i := 0; i < b.N; i++ {
		st, err := Open(ctx, filepath.Join(b.TempDir(), "state.db"))
		if err != nil {
			b.Fatalf("Open: %v", err)
		}
		if err := st.Migrate(ctx); err != nil {
			b.Fatalf("Migrate: %v", err)
		}
		st.Close()
	}
}
