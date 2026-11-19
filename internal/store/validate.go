package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// IsReadOnlyRecovery reports whether err is SQLite refusing a read-only open
// because a hot rollback journal (SQLITE_READONLY_ROLLBACK, 776) or unrecovered
// WAL (SQLITE_READONLY_RECOVERY, 264) needs a writable open to recover.
func IsReadOnlyRecovery(err error) bool {
	if err == nil {
		return false
	}
	var sqErr *sqlite.Error
	if errors.As(err, &sqErr) {
		switch sqErr.Code() {
		case sqlite3.SQLITE_READONLY_ROLLBACK, sqlite3.SQLITE_READONLY_RECOVERY:
			return true
		}
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "readonly database (776)") || strings.Contains(msg, "readonly database (264)")
}

// IsReadOnly reports whether err is any SQLite SQLITE_READONLY error (base code 8).
func IsReadOnly(err error) bool {
	if err == nil {
		return false
	}
	if IsReadOnlyRecovery(err) {
		return true
	}
	var sqErr *sqlite.Error
	if errors.As(err, &sqErr) && (sqErr.Code()&0xff) == sqlite3.SQLITE_READONLY {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "readonly database")
}

// ProbeHotJournal opens an existing database read-only and performs a minimal
// header read. It never creates a missing database or rolls back any journal.
// When an interrupted write left a hot rollback journal or unrecovered WAL,
// SQLite returns SQLITE_READONLY_ROLLBACK (776) or SQLITE_READONLY_RECOVERY (264).
func ProbeHotJournal(ctx context.Context, path string) error {
	ctx, cancel := context.WithTimeout(ctx, DiagnosticTimeout)
	defer cancel()
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&_pragma=busy_timeout(5000)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var version int
	return db.QueryRowContext(ctx, "PRAGMA schema_version").Scan(&version)
}

// recoverHotJournal opens an existing database in non-creating writable mode
// (mode=rw, never rwc) and triggers SQLite's hot-journal rollback / WAL
// recovery before closing it.
func recoverHotJournal(ctx context.Context, path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("state database is not a regular file")
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=rw&_pragma=busy_timeout(5000)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var version int
	return db.QueryRowContext(ctx, "PRAGMA schema_version").Scan(&version)
}

// ValidateInitialized uses SQLite's read-only mode, never the normal Open path:
// Open configures WAL and can create a database before initialization is known.
//
// Another window on the same project may be opening, migrating or closing the
// state at this moment, and SQLite reports that as SQLITE_BUSY. That is not
// damage: busy_timeout waits for it, bounded by DiagnosticTimeout, the same as
// every other handle on this database.
//
// If a prior open was interrupted (e.g. SIGQUIT/SIGKILL) leaving a hot rollback
// journal on an existing database, the read-only probe returns
// SQLITE_READONLY_ROLLBACK (776). In that case ValidateInitialized performs a
// single non-creating writable open (mode=rw) so SQLite rolls back the hot
// journal, then re-validates read-only.
func ValidateInitialized(ctx context.Context, path string) error {
	ctx, cancel := context.WithTimeout(ctx, DiagnosticTimeout)
	defer cancel()
	err := validateInitializedReadOnly(ctx, path)
	if !IsReadOnlyRecovery(err) {
		return err
	}
	if recErr := recoverHotJournal(ctx, path); recErr != nil {
		return fmt.Errorf("recover SQLite hot journal: %w (read-only open: %v)", recErr, err)
	}
	return validateInitializedReadOnly(ctx, path)
}

func validateInitializedReadOnly(ctx context.Context, path string) error {
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&_pragma=busy_timeout(5000)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	version, err := s.SchemaVersion(ctx)
	if err != nil {
		return err
	}
	if version < 1 || !SchemaVersionSupported(version) {
		return fmt.Errorf("unsupported state schema %d", version)
	}
	if _, err := s.Project(ctx); err != nil {
		return err
	}
	return s.QuickCheck(ctx)
}
