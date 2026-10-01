package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreDiagnostics(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "diagnostics.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, check := range []func(context.Context) error{s.QuickCheck, s.Integrity} {
		if err := check(ctx); err != nil {
			t.Fatal(err)
		}
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if err := check(cancelled); err == nil {
			t.Fatal("cancelled check passed")
		}
	}
	for _, table := range CountTables() {
		if _, err := s.Count(ctx, table); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"sqlite_master", "schema_migrations", "tasks; DROP TABLE tasks", "TASKS", ""} {
		if _, err := s.Count(ctx, table); err == nil {
			t.Fatalf("accepted %q", table)
		}
	}
	// Exhaust the connection pool so the deadline is exercised during acquisition.
	s.db.SetMaxOpenConns(1)
	conn, err := s.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []func(context.Context) error{s.QuickCheck, s.Integrity, func(ctx context.Context) error { _, err := s.Count(ctx, "tasks"); return err }} {
		bounded, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
		err := check(bounded)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("deadline: %v", err)
		}
	}
	started := time.Now()
	if err := s.QuickCheck(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("default timeout: %v", err)
	}
	if elapsed := time.Since(started); elapsed > DiagnosticTimeout+2*time.Second {
		t.Fatalf("timeout was not bounded: %s", elapsed)
	}
	conn.Close()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for _, check := range []func(context.Context) error{s.QuickCheck, s.Integrity} {
		if err := check(ctx); err == nil {
			t.Fatal("closed check passed")
		}
	}
	if _, err := s.Count(ctx, "tasks"); err == nil {
		t.Fatal("closed count passed")
	}
}

func TestStoreDiagnosticsCorruptFixture(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "corrupt.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Deliberately corrupt schema metadata on a disposable database. Both SQLite
	// checks must notice the invalid root page; the diagnostic paths never write.
	for _, statement := range []string{"CREATE TABLE broken (value TEXT)", "PRAGMA writable_schema=ON", "UPDATE sqlite_master SET rootpage=999999 WHERE name='broken'", "PRAGMA writable_schema=OFF"} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	s = &Store{db: db}
	defer s.Close()
	for _, check := range []func(context.Context) error{s.QuickCheck, s.Integrity} {
		err := check(ctx)
		if err == nil || !strings.Contains(err.Error(), "malformed") && !strings.Contains(err.Error(), "invalid page number") {
			t.Fatalf("corruption undetected: %v", err)
		}
	}
}
