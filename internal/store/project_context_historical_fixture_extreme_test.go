//go:build !governed

package store

import "testing"

// Historical fixtures first migrate the CURRENT schema, then reconstruct an
// earlier schema/ledger. Future-only structures must also be removed from the
// isolated fixture. Production migration deliberately refuses existing tables
// without their committed ledger rather than adopting potentially forged DDL.
func resetProjectContext95HistoricalFixture(t *testing.T, s *Store) {
	t.Helper()
	tables := []string{"extreme_project_context_receipts", "extreme_project_contract_required_refs", "extreme_project_contract_active", "extreme_project_contract_revisions"}
	for _, table := range tables {
		var exists, count int
		if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists == 0 {
			continue
		}
		if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("refusing to remove retained project context from historical fixture: %s", table)
		}
	}
	for _, table := range tables {
		if _, err := s.db.ExecContext(t.Context(), "DROP TABLE IF EXISTS "+table); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.ExecContext(t.Context(), "DROP INDEX IF EXISTS extreme_project_accepted_memory_inventory"); err != nil {
		t.Fatal(err)
	}
}
