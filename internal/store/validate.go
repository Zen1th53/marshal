package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
)

// ValidateInitialized uses SQLite's read-only mode, never the normal Open path:
// Open configures WAL and can create a database before initialization is known.
//
// Another window on the same project may be opening, migrating or closing the
// state at this moment, and SQLite reports that as SQLITE_BUSY. That is not
// damage: busy_timeout waits for it, bounded by DiagnosticTimeout, the same as
// every other handle on this database.
func ValidateInitialized(ctx context.Context, path string) error {
	ctx, cancel := context.WithTimeout(ctx, DiagnosticTimeout)
	defer cancel()
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
