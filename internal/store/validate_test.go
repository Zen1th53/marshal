package store

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestValidateInitializedNeverCreatesMissingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	if err := ValidateInitialized(t.Context(), path); err == nil {
		t.Fatal("accepted missing canonical state")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("validation created canonical state: %v", err)
	}
}

// Two windows on one project: one validates the state while another process
// opens, writes and closes it. A busy moment in the other window is not
// damage, so validation must wait for it rather than report the files as
// damaged.
func TestValidateInitializedWaitsForAnotherWindowsWrites(t *testing.T) {
	ctx := context.Background()
	if path := os.Getenv("MARSHAL_TEST_VALIDATE_WRITER"); path != "" {
		validateWriterLoop(ctx, path)
		return
	}
	path := filepath.Join(t.TempDir(), "state.db")
	st, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.InitProject(ctx, model.Project{ID: "proj-validate", Repository: "/repo/validate", DefaultBranch: "main", PackVersion: "6.0.0"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// SQLite locks are per process, so the other window must be another
	// process for its locks to meet this one the way two terminals do.
	var writers []*exec.Cmd
	for range 2 {
		cmd := exec.Command(os.Args[0], "-test.run=^TestValidateInitializedWaitsForAnotherWindowsWrites$")
		cmd.Env = append(os.Environ(), "MARSHAL_TEST_VALIDATE_WRITER="+path)
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		writers = append(writers, cmd)
	}
	t.Cleanup(func() {
		for _, cmd := range writers {
			cmd.Process.Kill()
			cmd.Wait()
		}
	})
	failures, attempts := 0, 0
	var first error
	for stop := time.Now().Add(3 * time.Second); time.Now().Before(stop); attempts++ {
		if err := ValidateInitialized(ctx, path); err != nil {
			failures++
			if first == nil {
				first = err
			}
		}
	}
	if failures != 0 {
		t.Fatalf("validation failed %d of %d times while another window used the state: %v", failures, attempts, first)
	}
}

// validateWriterLoop is the other window: it opens the state, writes and
// migrates it the way a window opening the project does, and closes it.
func validateWriterLoop(ctx context.Context, path string) {
	for stop := time.Now().Add(5 * time.Second); time.Now().Before(stop); {
		writer, err := Open(ctx, path)
		if err != nil {
			continue
		}
		writer.Migrate(ctx)
		writer.Close()
	}
}

// snapshotHotRollbackJournal builds a real SQLite database with an uncommitted
// hot rollback journal (-journal) and no active locks, simulating a process
// killed with SIGQUIT/SIGKILL mid-transaction.
func snapshotHotRollbackJournal(t *testing.T) string {
	t.Helper()
	ctx := t.Context()
	dir := t.TempDir()
	src := filepath.Join(dir, "src.db")
	st, err := Open(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.InitProject(ctx, model.Project{ID: "proj-hot", Repository: "/repo/hot", DefaultBranch: "main", PackVersion: "6.0.0"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, "PRAGMA journal_mode = DELETE"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err := sql.Open("sqlite", src)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.ExecContext(ctx, "PRAGMA cache_size = 1"); err != nil {
		t.Fatal(err)
	}
	tx, err := raw.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "UPDATE projects SET default_branch = 'uncommitted-branch'"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "CREATE TABLE hot_spill (id INTEGER PRIMARY KEY, body TEXT)"); err != nil {
		t.Fatal(err)
	}
	blob := make([]byte, 4096)
	for i := range blob {
		blob[i] = 'x'
	}
	for i := 0; i < 16; i++ {
		if _, err := tx.ExecContext(ctx, "INSERT INTO hot_spill(body) VALUES (?)", string(blob)); err != nil {
			t.Fatal(err)
		}
	}

	dbBytes, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	journalBytes, err := os.ReadFile(src + "-journal")
	if err != nil {
		t.Fatalf("expected rollback journal to exist while tx is open: %v", err)
	}
	if len(journalBytes) == 0 {
		t.Fatal("expected non-empty rollback journal")
	}

	dst := filepath.Join(t.TempDir(), "state.db")
	if err := os.WriteFile(dst, dbBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst+"-journal", journalBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return dst
}

func TestValidateInitializedRecoversHotRollbackJournal(t *testing.T) {
	ctx := t.Context()
	path := snapshotHotRollbackJournal(t)

	// A pure read-only probe must observe SQLITE_READONLY_ROLLBACK (776).
	if err := ProbeHotJournal(ctx, path); !IsReadOnlyRecovery(err) {
		t.Fatalf("ProbeHotJournal = %v, want SQLITE_READONLY_ROLLBACK", err)
	}

	// ValidateInitialized rolls back the hot journal via a non-creating writable
	// open and then succeeds on the read-only validation.
	if err := ValidateInitialized(ctx, path); err != nil {
		t.Fatalf("ValidateInitialized failed to recover hot journal: %v", err)
	}
	if err := ProbeHotJournal(ctx, path); err != nil {
		t.Fatalf("ProbeHotJournal after recovery = %v", err)
	}
	st, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	proj, err := st.Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if proj.DefaultBranch != "main" {
		t.Fatalf("uncommitted branch survived hot-journal rollback: got %q, want %q", proj.DefaultBranch, "main")
	}
}

func TestValidateInitializedHotJournalUnwritableFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permissions")
	}
	ctx := t.Context()
	path := snapshotHotRollbackJournal(t)
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	err := ValidateInitialized(ctx, path)
	if err == nil {
		t.Fatal("expected error when hot journal cannot be rolled back on read-only file")
	}
	if !IsReadOnly(err) && !IsReadOnlyRecovery(err) {
		t.Fatalf("expected SQLITE_READONLY error, got: %v", err)
	}
}
