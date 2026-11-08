//go:build !governed

package app

import (
	"database/sql"
	"errors"
	"github.com/Zen1th53/marshal/internal/store"
	"path/filepath"
	"testing"
)

// Recreate a full historical v91 store from a fresh, otherwise unmodified
// bootstrap fixture. Only empty private tables/ledger steps are removed.
func historicalPrivate91Fixture(t *testing.T) string {
	t.Helper()
	repo := runtimeRepo(t)
	layout, err := Bootstrap(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", layout.Database)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"extreme_project_context_receipts", "extreme_project_contract_required_refs", "extreme_project_contract_active", "extreme_project_contract_revisions", "extreme_memory_restore_history", "extreme_memory_restore_previews", "extreme_memory_snapshots", "extreme_cell_command_phases", "extreme_approved_cell_history", "extreme_approved_cells"} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Fatal("historical fixture private tables must be empty")
		}
		if _, err := db.Exec(`DROP TABLE ` + table); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version>91`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return repo.Path()
}
func privateSchemaVersion(t *testing.T, root string) int {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(root, ".marshal", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	return version
}
func TestPrivatePendingMigrationRequiresLifecycleOwner(t *testing.T) {
	for _, entry := range []string{"open", "bootstrap"} {
		t.Run(entry, func(t *testing.T) {
			root := historicalPrivate91Fixture(t)
			lock, err := acquireRuntimeOwner(filepath.Join(root, ".marshal"))
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			if entry == "open" {
				rt, e := Open(t.Context(), root)
				if rt != nil {
					rt.Close()
				}
				err = e
			} else {
				_, err = Bootstrap(t.Context(), root)
			}
			if !errors.Is(err, ErrRuntimeOwned) {
				t.Errorf("pending private migration did not refuse live owner: %v", err)
			}
			if v := privateSchemaVersion(t, root); v != 91 {
				t.Fatalf("live owner store migrated underneath it: v%d", v)
			}
		})
	}
}
func TestPrivatePendingMigrationSucceedsAfterOwnedClose(t *testing.T) {
	root := historicalPrivate91Fixture(t)
	rt, err := Open(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !rt.IsLifecycleOwner() || privateSchemaVersion(t, root) != store.LatestSchemaVersion {
		t.Fatal("owned reopen did not migrate")
	}
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := Open(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if !next.IsLifecycleOwner() {
		t.Fatal("reopen lost lifecycle ownership")
	}
}
func TestPrivateSameSchemaBootstrapPreservesLiveOwner(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	owner, err := Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if _, err := Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	second, err := Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if !owner.IsLifecycleOwner() || second.IsLifecycleOwner() {
		t.Fatal("same-schema attachment changed ownership")
	}
}

func TestPrivateBootstrapUpgradeReleasesTemporaryOwnership(t *testing.T) {
	root := historicalPrivate91Fixture(t)
	if _, err := Bootstrap(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	if privateSchemaVersion(t, root) != store.LatestSchemaVersion {
		t.Fatal("bootstrap did not migrate owned historical store")
	}
	rt, err := Open(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	if !rt.IsLifecycleOwner() {
		t.Fatal("bootstrap retained its temporary lifecycle lock")
	}
}

func TestPrivateNonOwnerUnknownSchemaDoesNotCreateLedger(t *testing.T) {
	root := historicalPrivate91Fixture(t)
	lock, err := acquireRuntimeOwner(filepath.Join(root, ".marshal"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	db, err := sql.Open("sqlite", filepath.Join(root, ".marshal", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`DROP TABLE schema_migrations`); err != nil {
		t.Fatal(err)
	}
	if _, err := Bootstrap(t.Context(), root); !errors.Is(err, ErrRuntimeOwned) {
		t.Fatalf("unknown live-owned schema was not refused: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='schema_migrations'`).Scan(&n); err != nil || n != 0 {
		t.Fatal("non-owner created the migration ledger")
	}
}
