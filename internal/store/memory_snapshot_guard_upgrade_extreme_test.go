//go:build !governed

package store

import (
	"strconv"
	"testing"
)

func TestMemorySnapshotGuardMigrationUpgradesExistingPrivateLedger(t *testing.T) {
	for _, version := range []int{92, 93} {
		t.Run(strconv.Itoa(version), func(t *testing.T) {
			s, path, b, ids := memorySnapshotFixture(t)
			snapshot, err := s.CreateMemorySnapshot(t.Context(), b, "SNAP-upgrade", ids)
			if err != nil {
				t.Fatal(err)
			}
			preview, err := s.PreviewMemorySnapshot(t.Context(), b, snapshot.ID, 1, snapshot.Digest, "PREVIEW-upgrade")
			if err != nil {
				t.Fatal(err)
			}
			// Recreate the actual pre-guard private schema/ledger in this isolated
			// fixture. Bodies and preview remain durable across the upgrade.
			if _, err := s.db.Exec(`DROP TRIGGER extreme_memory_preview_immutable_update; DROP TRIGGER extreme_memory_preview_immutable_delete`); err != nil {
				t.Fatal(err)
			}
			if version == 92 {
				if _, err := s.db.Exec(`DROP TABLE extreme_approved_cell_history; DROP TABLE extreme_cell_command_phases; DROP TABLE extreme_approved_cells`); err != nil {
					t.Fatal(err)
				}
			}
			resetProjectContext95HistoricalFixture(t, s)
			// The historical private ledger recorded these steps as v92 and v93;
			// the canonical steps are absent until the upgrade adopts them.
			if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version>91`); err != nil {
				t.Fatal(err)
			}
			for legacy := 92; legacy <= version; legacy++ {
				if _, err := s.db.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)`, legacy, utcNow()); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reopened.Close() })
			if err := reopened.Migrate(t.Context()); err != nil {
				t.Fatal(err)
			}
			var ledger int
			if err := reopened.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&ledger); err != nil || ledger != LatestSchemaVersion {
				t.Fatalf("upgrade ledger=%d %v", ledger, err)
			}
			if _, err := reopened.db.Exec(`UPDATE extreme_memory_restore_previews SET preview_json='{}'`); err == nil {
				t.Fatal("existing private preview lacks installed guard")
			}
			if _, err := reopened.db.Exec(`DELETE FROM extreme_memory_restore_previews`); err == nil {
				t.Fatal("existing private preview deletion lacks installed guard")
			}
			retained, err := reopened.GetMemorySnapshot(t.Context(), b, snapshot.ID, 1)
			if err != nil || retained.Digest != snapshot.Digest || retained.Records[0].Body != snapshot.Records[0].Body {
				t.Fatalf("upgrade lost full archive: %+v %v", retained, err)
			}
			if _, err := reopened.RestoreMemorySnapshot(t.Context(), b, preview); err != nil {
				t.Fatalf("guard upgrade broke retained exact preview: %v", err)
			}
			if err := reopened.Migrate(t.Context()); err != nil {
				t.Fatalf("upgrade not idempotent: %v", err)
			}
		})
	}
}
