package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Zen1th53/marshal/internal/evidence"
	_ "modernc.org/sqlite"
)

type Store struct {
	db        *sql.DB
	sanitizer evidence.Sanitizer
}

func Open(ctx context.Context, path string) (*Store, error) {
	return OpenWithSanitizer(ctx, path, evidence.NewStrictSanitizer(evidence.SanitizerConfig{}))
}

func OpenWithSanitizer(ctx context.Context, path string, sanitizer evidence.Sanitizer) (*Store, error) {
	if sanitizer == nil {
		return nil, fmt.Errorf("evidence sanitizer is required")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open SQLite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	st := &Store{db: db, sanitizer: sanitizer}
	if err := st.configure(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return st, nil
}

func (s *Store) configure(ctx context.Context) error {
	for _, statement := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("configure SQLite with %q: %w", statement, err)
		}
	}
	var enabled int
	if err := s.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&enabled); err != nil {
		return fmt.Errorf("verify SQLite foreign keys: %w", err)
	}
	if enabled != 1 {
		return fmt.Errorf("verify SQLite foreign keys: disabled")
	}
	return nil
}

func (s *Store) Close() error {
	return s.db.Close()
}
