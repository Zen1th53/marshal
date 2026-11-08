package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/evidence"
	_ "modernc.org/sqlite"
)

// SQLite serializes writers per database. Multiple Store handles can therefore
// observe transient SQLITE_BUSY even with busy_timeout configured; retain a
// bounded retry window for transactional operations rather than leaking a
// scheduler-dependent lock error to callers.
const sqliteBusyRetries = 32

func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "database is locked") || strings.Contains(message, "database table is locked")
}

func waitSQLiteRetry(ctx context.Context, attempt int) error {
	delay := time.Duration(attempt+1) * 10 * time.Millisecond
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type Store struct {
	db               *sql.DB
	architectureFile *architectureSQLiteFile
	sanitizer        evidence.Sanitizer
	authorizer       evidence.Authorizer
	metrics          *evidence.MetricsRecorder
	// credentialDecisions caches CredentialDecisionEvents per project/provider.
	credentialDecisions credentialDecisionCache
}

func Open(ctx context.Context, path string) (*Store, error) {
	return OpenWithSanitizer(ctx, path, evidence.NewStrictSanitizer(evidence.SanitizerConfig{}))
}

func OpenWithSanitizer(ctx context.Context, path string, sanitizer evidence.Sanitizer) (*Store, error) {
	return OpenWithSecurity(ctx, path, sanitizer, nil)
}

func OpenWithSecurity(ctx context.Context, path string, sanitizer evidence.Sanitizer, authorizer evidence.Authorizer) (*Store, error) {
	return OpenWithObservability(ctx, path, sanitizer, authorizer, nil)
}

// OpenWithObservability attaches an optional bounded operational projection.
// Metrics never participate in authorization or persistence decisions.
func OpenWithObservability(ctx context.Context, path string, sanitizer evidence.Sanitizer, authorizer evidence.Authorizer, metrics *evidence.MetricsRecorder) (*Store, error) {
	if sanitizer == nil {
		return nil, fmt.Errorf("evidence sanitizer is required")
	}
	dsn, architectureFile, err := architectureSQLiteDSN(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", withConnectionSettings(dsn))
	if err != nil {
		return nil, fmt.Errorf("open SQLite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	st := &Store{db: db, architectureFile: architectureFile, sanitizer: sanitizer, authorizer: authorizer, metrics: metrics}
	if err := st.configure(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return st, nil
}

// Metrics returns the configured detached operational recorder, if any.
func (s *Store) Metrics() *evidence.MetricsRecorder { return s.metrics }

// withConnectionSettings puts the per-connection settings in the DSN, so the
// driver applies them to every connection the pool opens (busy_timeout first),
// not only to the one that ran configure.
//
// _txlock=immediate makes every read-write transaction BEGIN IMMEDIATE.
// Several Store handles (runtimes, processes) share one database. A deferred
// transaction that reads and then writes must upgrade its read snapshot to the
// write lock, and SQLite never runs the busy handler for that upgrade: it fails
// at once with SQLITE_BUSY while another handle writes, whatever busy_timeout
// is. Taking the write lock at BEGIN waits for it under busy_timeout instead.
// Read-only transactions (sql.TxOptions{ReadOnly: true}) stay deferred.
func withConnectionSettings(dsn string) string {
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	return dsn + separator + "_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_txlock=immediate"
}

func (s *Store) configure(ctx context.Context) error {
	// The DSN already set busy_timeout on this connection, so switching to
	// WAL waits for a lock another process holds instead of failing at once.
	for _, statement := range []string{
		"PRAGMA busy_timeout = 5000",
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
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
