//go:build !governed

package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/statepath"
)

// Produce an actual schema-90 database using the historical migration chain,
// rather than only changing a current database's ledger. Canonical steps begin
// after 91; temporarily omitting their registry yields the old public shape.
func TestPublicV007StateUpgradesInPlace(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".marshal")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	s, err := Open(t.Context(), filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	registry := canonicalMigrations
	func() {
		canonicalMigrations = map[int]func(context.Context, *sql.Tx) error{}
		defer func() { canonicalMigrations = registry }()
		if err := s.Migrate(t.Context()); err != nil {
			t.Fatal(err)
		}
	}()
	if _, err := s.db.Exec("DROP TABLE marshal_proposals; DELETE FROM schema_migrations WHERE version>90"); err != nil {
		t.Fatal(err)
	}
	if err := s.InitProject(t.Context(), model.Project{ID: "PROJECT-old-public", Repository: root, DefaultBranch: "main", PackVersion: "0.0.7"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO tasks(task_id,project_id,title,status,risk,created_at,updated_at) VALUES('TASK-old-public','PROJECT-old-public','retained public task','ready','R0',?,?)`, utcNow(), utcNow()); err != nil {
		t.Fatal(err)
	}

	before, err := os.Stat(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := statepath.Prepare(root); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(filepath.Join(dir, "state.db"))
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("replaced database", err)
	}
	var version int
	var repository string
	if err := s.db.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil || version != LatestSchemaVersion {
		t.Fatal(version, err)
	}
	if err := s.db.QueryRow("SELECT repository FROM projects WHERE project_id='PROJECT-old-public'").Scan(&repository); err != nil || repository != root {
		t.Fatal("lost old public project", repository, err)
	}
	var title string
	if err := s.db.QueryRow("SELECT title FROM tasks WHERE task_id='TASK-old-public'").Scan(&title); err != nil || title != "retained public task" {
		t.Fatal("lost old public task", title, err)
	}
}
