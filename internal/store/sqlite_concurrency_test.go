package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/statepath"
)

// Two runtimes on one project open separate Store handles on one database.
// A write transaction that reads first (a receipt rechecks its grant before
// inserting) must wait for the other handle's writer, not fail at its first
// write: SQLite never invokes the busy handler when a deferred transaction
// upgrades its read snapshot to a write lock, so busy_timeout alone cannot help.
func TestWriteTransactionWaitsForAnotherHandlesWriter(t *testing.T) {
	// The project database (under the state directory) opens through the
	// identity VFS URI; any other path opens as a plain file name.
	project := filepath.Join(t.TempDir(), statepath.DirName)
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(project, "state.db"), filepath.Join(t.TempDir(), "plain.db")} {
		t.Run(filepath.Base(path), func(t *testing.T) { writeWaitsForOtherHandle(t, path) })
	}
}

func writeWaitsForOtherHandle(t *testing.T, path string) {
	ctx := context.Background()
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := first.db.ExecContext(ctx, `CREATE TABLE busy_probe(owner TEXT)`); err != nil {
		t.Fatal(err)
	}
	holder, err := first.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback()
	if _, err := holder.ExecContext(ctx, `INSERT INTO busy_probe(owner) VALUES('first')`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		tx, err := second.db.BeginTx(ctx, nil)
		if err != nil {
			done <- err
			return
		}
		defer tx.Rollback()
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM busy_probe`).Scan(&n); err != nil {
			done <- err
			return
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO busy_probe(owner) VALUES('second')`); err != nil {
			done <- err
			return
		}
		done <- tx.Commit()
	}()
	time.Sleep(100 * time.Millisecond)
	if err := holder.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("second handle's write transaction: %v", err)
	}
	var n int
	if err := first.db.QueryRowContext(ctx, `SELECT count(*) FROM busy_probe`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("rows=%d err=%v", n, err)
	}
}

// Every pooled connection carries the per-connection settings, not only the
// one that happened to run configure.
func TestEveryConnectionCarriesBusyTimeoutAndForeignKeys(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.db.SetMaxIdleConns(0) // force a fresh connection per statement
	for range 3 {
		var timeout, fk int
		if err := st.db.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&timeout); err != nil {
			t.Fatal(err)
		}
		if err := st.db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil {
			t.Fatal(err)
		}
		if timeout < 5000 || fk != 1 {
			t.Fatalf("connection busy_timeout=%d foreign_keys=%d", timeout, fk)
		}
	}
}
