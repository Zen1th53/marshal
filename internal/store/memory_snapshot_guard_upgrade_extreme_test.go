//go:build !governed

package store

import (
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// memorySnapshotFixture opens a migrated private store with one project and
// two durable records.
func memorySnapshotFixture(t *testing.T) (*Store, string, string, []string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	const project = "project"
	if err := s.InitProject(t.Context(), model.Project{ID: project, Repository: "repo", DefaultBranch: "main", PackVersion: "test"}); err != nil {
		t.Fatal(err)
	}
	ids := []string{"MEM-one", "MEM-two"}
	now := time.Now().UTC()
	for _, id := range ids {
		rec := model.MemoryRecordV2{ID: id, ProjectID: project, Kind: model.MemoryKindDecision, Lifecycle: model.MemoryDurable, Confidence: model.ConfidenceVerified, Authority: model.AuthorityVerified, Title: id, Body: "snapshot body " + id, Scope: "project", ScopeID: project, Source: model.MemorySource{Kind: "test", Reference: "original"}, ObservedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now}
		if err := s.WriteMemoryV2(t.Context(), rec); err != nil {
			t.Fatal(err)
		}
	}
	return s, path, project, ids
}

// Older private databases may hold snapshot and preview rows written by an
// earlier build. The guard migration must keep them and install the guards.
func TestMemorySnapshotGuardMigrationUpgradesExistingPrivateLedger(t *testing.T) {
	for _, version := range []int{92, 93} {
		t.Run(strconv.Itoa(version), func(t *testing.T) {
			s, path, project, _ := memorySnapshotFixture(t)
			if _, err := s.db.Exec(`INSERT INTO extreme_memory_snapshots(project_id,snapshot_id,version,session_id,actor,digest,snapshot_json,created_at) VALUES(?,?,1,'session','operator','digest','{"records":[]}',?)`, project, "SNAP-upgrade", utcNow()); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`INSERT INTO extreme_memory_restore_previews(project_id,preview_id,snapshot_id,version,session_id,actor,command_key,preview_json) VALUES(?,?,?,1,'session','operator','key','{}')`, project, "PREVIEW-upgrade", "SNAP-upgrade"); err != nil {
				t.Fatal(err)
			}
			// Recreate the actual pre-guard private schema/ledger in this isolated
			// fixture. Snapshot and preview rows remain durable across the upgrade.
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
			if _, err := reopened.db.Exec(`UPDATE extreme_memory_restore_previews SET preview_json='{"changed":true}'`); err == nil {
				t.Fatal("existing private preview lacks installed guard")
			}
			if _, err := reopened.db.Exec(`DELETE FROM extreme_memory_restore_previews`); err == nil {
				t.Fatal("existing private preview deletion lacks installed guard")
			}
			var snapshots, previews int
			if err := reopened.db.QueryRow(`SELECT (SELECT count(*) FROM extreme_memory_snapshots WHERE snapshot_id='SNAP-upgrade'),(SELECT count(*) FROM extreme_memory_restore_previews WHERE preview_id='PREVIEW-upgrade')`).Scan(&snapshots, &previews); err != nil || snapshots != 1 || previews != 1 {
				t.Fatalf("upgrade lost retained rows: snapshots=%d previews=%d %v", snapshots, previews, err)
			}
			if err := reopened.Migrate(t.Context()); err != nil {
				t.Fatalf("upgrade not idempotent: %v", err)
			}
		})
	}
}
