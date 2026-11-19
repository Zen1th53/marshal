package projectknowledge

import (
	"context"
	"database/sql"
	"errors"
	"github.com/Zen1th53/marshal/internal/statepath"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchitectureContextRefreshRestartAndCurrentBytes(t *testing.T) {
	root, index := fixture(t)
	write(t, root, "PROJECT.md", "# Purpose\n\nKeep engineering decisions.\n")
	write(t, root, "docs/architecture.md", "# Architecture\n\nThe store owns durable state.\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "project purpose and architecture")
	if err := index.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	first, err := index.ArchitectureContext(t.Context(), 65536)
	if err != nil || first.Status != "ready" || len(first.Architecture) != 1 || len(first.Purpose) != 2 {
		t.Fatalf("initial context: %+v %v", first, err)
	}
	if err := index.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(root, filepath.Join(root, statepath.DirName))
	if err != nil {
		t.Fatal(err)
	}
	next, err := reopened.ArchitectureContext(t.Context(), 65536)
	if err != nil || next.Snapshot.Digest != first.Snapshot.Digest {
		t.Fatal("restart lost source binding")
	}
	write(t, root, "docs/architecture.md", "# Architecture\n\nThe runtime owns durable state.\n")
	if _, err := index.ArchitectureContext(t.Context(), 65536); !errors.Is(err, ErrStale) {
		t.Fatal("old architecture served after tracked edit")
	}
	if err := index.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	next, err = index.ArchitectureContext(t.Context(), 65536)
	if err != nil || !strings.Contains(next.Architecture[0].Text, "runtime owns") || next.Snapshot.Digest == first.Snapshot.Digest {
		t.Fatal("architecture did not follow edit")
	}
	if _, err := index.ArchitectureContext(t.Context(), 1); !errors.Is(err, ErrContextBudget) {
		t.Fatal("invalid byte budget accepted")
	}
	write(t, root, "docs/architecture.md", "# Architecture\n"+strings.Repeat("A bounded architecture sentence.\n", 120))
	if err := index.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := index.ArchitectureContext(t.Context(), 1024); !errors.Is(err, ErrContextBudget) {
		t.Fatal("architecture silently truncated to budget")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := index.Ensure(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestArchitectureMissingDeletedPrivateAndCorruptSources(t *testing.T) {
	root, index := fixture(t)
	if err := index.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	packet, err := index.ArchitectureContext(t.Context(), 65536)
	if err != nil || packet.Status != "needs_architecture" {
		t.Fatal("invented architecture")
	}
	write(t, root, "docs/architecture.md", "# Architecture\n\nRuntime owns state.\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "architecture")
	if err := index.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "docs/architecture.md")); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	packet, err = index.ArchitectureContext(t.Context(), 65536)
	if err != nil || len(packet.Architecture) != 0 {
		t.Fatal("deleted architecture reused")
	}
	if err := os.Remove(filepath.Join(root, "README.md")); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	packet, err = index.ArchitectureContext(t.Context(), 65536)
	if err != nil || packet.Status != "needs_purpose" {
		t.Fatal("invented project purpose")
	}
	write(t, root, "docs/architecture.md", "# Architecture\napi_key = \"sk-test-synthetic-credential-example\"\n")
	if err := index.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	packet, err = index.ArchitectureContext(t.Context(), 65536)
	if err != nil || len(packet.Architecture) != 0 {
		t.Fatal("private source exposed")
	}
	if err := os.WriteFile(index.database, []byte("not a database"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(t.Context()); err == nil {
		t.Fatal("corrupt cache silently repaired")
	}
}

func TestArchitectureWorktreeCacheNeverTouchesTaskSource(t *testing.T) {
	root, _ := fixture(t)
	state := filepath.Join(root, statepath.DirName)
	if err := os.MkdirAll(state, 0700); err != nil {
		t.Fatal(err)
	}
	tree := filepath.Join(state, "worktrees", "TASK-context")
	git(t, root, "worktree", "add", "-b", "context-task", tree, "HEAD")
	index, err := OpenWorktree(tree, root, state)
	if err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := index.ArchitectureContext(t.Context(), 65536); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tree, statepath.DirName)); !os.IsNotExist(err) {
		t.Fatal("derived cache entered the worker checkout")
	}
	if _, err := OpenWorktree(root, root, state); !errors.Is(err, ErrBinding) {
		t.Fatal("owner checkout accepted as a task")
	}
	foreign := t.TempDir()
	if _, err := OpenWorktree(foreign, root, state); !errors.Is(err, ErrBinding) {
		t.Fatal("foreign worktree accepted")
	}
	if err := os.Symlink(tree, filepath.Join(state, "worktrees", "alias")); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenWorktree(filepath.Join(state, "worktrees", "alias"), root, state); !errors.Is(err, ErrBinding) {
		t.Fatal("symlink worktree accepted")
	}
}

func TestEnsureRecoversHotRollbackJournal(t *testing.T) {
	ctx := t.Context()
	_, index := fixture(t)
	if err := index.Ensure(ctx); err != nil {
		t.Fatal(err)
	}

	// Simulate SIGQUIT mid-refresh leaving a hot rollback journal (-journal)
	// with dirty pages spilled to disk.
	raw, err := sql.Open("sqlite", index.database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, "PRAGMA cache_size = 1"); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	tx, err := raw.BeginTx(ctx, nil)
	if err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE metadata SET root = 'interrupted' WHERE id = 1"); err != nil {
		tx.Rollback()
		raw.Close()
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "CREATE TABLE hot_spill (id INTEGER PRIMARY KEY, body TEXT)"); err != nil {
		tx.Rollback()
		raw.Close()
		t.Fatal(err)
	}
	blob := strings.Repeat("x", 4096)
	for i := 0; i < 16; i++ {
		if _, err := tx.ExecContext(ctx, "INSERT INTO hot_spill(body) VALUES (?)", blob); err != nil {
			tx.Rollback()
			raw.Close()
			t.Fatal(err)
		}
	}
	dbBytes, err := os.ReadFile(index.database)
	if err != nil {
		tx.Rollback()
		raw.Close()
		t.Fatal(err)
	}
	journalBytes, err := os.ReadFile(index.database + "-journal")
	if err != nil {
		tx.Rollback()
		raw.Close()
		t.Fatalf("expected rollback journal during open tx: %v", err)
	}
	_ = tx.Rollback()
	_ = raw.Close()

	if err := os.WriteFile(index.database, dbBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(index.database+"-journal", journalBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	// Read-only Query fails with SQLITE_READONLY_ROLLBACK (776).
	if _, err := index.Query(ctx, Query{Mode: "overview", Limit: 1}); !isReadOnlyRecovery(err) {
		t.Fatalf("Query with hot journal = %v, want SQLITE_READONLY_ROLLBACK", err)
	}
	// Ensure recovers by rolling back the hot journal via a writable open and refreshing.
	if err := index.Ensure(ctx); err != nil {
		t.Fatalf("Ensure failed to recover hot journal: %v", err)
	}
	if _, err := index.Query(ctx, Query{Mode: "overview", Limit: 1}); err != nil {
		t.Fatalf("Query after recovery = %v", err)
	}
}
