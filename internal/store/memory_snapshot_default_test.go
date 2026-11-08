//go:build governed

package store

import "testing"

func TestCommunityHasNoPrivateMemorySnapshotSchema(t *testing.T) {
	s := openTestStore(t)
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, version := range []int{schemaExtremeMemorySnapshots, schemaExtremeApprovedCells, schemaExtremeSnapshotGuards, schemaExtremeProjectContext} {
		if canonicalMigrations[version] != nil {
			t.Fatalf("private migration %d registered in Community", version)
		}
	}
	var count int
	if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_master WHERE type='table' AND name LIKE 'extreme_memory_%'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("private memory tables leaked into Community: %d %v", count, err)
	}
}
