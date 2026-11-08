// Package store is the host-owned evidence store: published evidence
// generations (immutable snapshot manifests over reusable per-file
// artifacts), reader pins, receipts, the delivery ledger and revocations,
// in SQLite (pure-Go driver, no cgo).
//
// Rules (SPEC §3, §4):
//   - the store lives outside the target repository (default
//     $XDG_CACHE_HOME/code-evidence/<repo-id>/) and is never opened from a
//     path inside it;
//   - it stores structure and hashes only, never source text;
//   - busy_timeout is set before WAL mode, transactions are short and
//     writes use BEGIN IMMEDIATE; one in-process writer at a time;
//   - artifacts are written before a generation's manifest is published
//     in one atomic transaction; reader pins live in their own rows and
//     never hold a database transaction open;
//   - revocation is durable and overrides pins; purge deletes dependent
//     rows, vacuums and truncates the WAL (see Forget).
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	_ "modernc.org/sqlite"
)

// SchemaVersion is the current schema version.
const SchemaVersion = 2

// Store is an open evidence store.
type Store struct {
	db   *sql.DB
	path string
	wmu  sync.Mutex // single writer within the process
	// gate drains in-process readers and writers during a purge: every
	// operation holds it shared, Purge holds it exclusively.
	gate sync.RWMutex
	// publishing counts in-flight publications (GC leaves orphans alone).
	publishing atomic.Int32
}

// RepoID derives a stable store key from the canonical repository root.
func RepoID(repoAbs string) string {
	h := sha256.Sum256([]byte("cec/repo/v1\x00" + repoAbs))
	return hex.EncodeToString(h[:8])
}

// DefaultDir returns $XDG_CACHE_HOME/code-evidence/<repo-id> (falling back
// to os.UserCacheDir).
func DefaultDir(repoAbs string) (string, error) {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		d, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		base = d
	}
	return filepath.Join(base, "code-evidence", RepoID(repoAbs)), nil
}

// ErrInsideRepo is returned when a store path lies inside the repository.
var ErrInsideRepo = errors.New("store: refusing to use a store inside the target repository")

func canonical(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	// Resolve the longest existing prefix so a symlinked parent cannot
	// smuggle the store into the repository.
	rest := ""
	cur := abs
	for {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(r, rest), nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs, nil
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

func within(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// Open opens (creating if needed) the store in dir. repoAbs is the target
// repository root; dir must not be inside it.
func Open(ctx context.Context, dir, repoAbs string) (*Store, error) {
	cdir, err := canonical(dir)
	if err != nil {
		return nil, err
	}
	if repoAbs != "" {
		crepo, err := canonical(repoAbs)
		if err != nil {
			return nil, err
		}
		if within(cdir, crepo) {
			return nil, ErrInsideRepo
		}
	}
	if err := os.MkdirAll(cdir, 0o700); err != nil {
		return nil, err
	}
	st, err := os.Lstat(cdir)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("store: %s is not a directory", cdir)
	}
	if err := checkOwner(st); err != nil {
		return nil, err
	}
	path := filepath.Join(cdir, "store.db")
	if fi, err := os.Lstat(path); err == nil && !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("store: %s is not a regular file", path)
	}
	// busy_timeout must precede journal_mode(WAL): switching to WAL needs
	// a lock and must wait rather than fail under contention.
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)&_pragma=secure_delete(1)&_pragma=temp_store(2)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	s := &Store{db: db, path: path}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	return s, nil
}

// Path returns the database file path.
func (s *Store) Path() string { return s.path }

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

var schemaV2 = []string{
	// Reusable per-file artifacts: one row per distinct (path, blob,
	// size, skip state), shared by every generation that contains it.
	`CREATE TABLE IF NOT EXISTS artifacts (
		artifact_id TEXT PRIMARY KEY,
		path        TEXT NOT NULL,
		sha256      TEXT NOT NULL,
		size        INTEGER NOT NULL,
		skipped     TEXT NOT NULL DEFAULT ''
	) WITHOUT ROWID`,
	`CREATE INDEX IF NOT EXISTS artifacts_sha ON artifacts(sha256)`,
	// Published generations: immutable manifests (generation id = policy
	// scoped snapshot id). seq orders publication for GC.
	`CREATE TABLE IF NOT EXISTS generations (
		gen_id      TEXT PRIMARY KEY,
		policy_id   TEXT NOT NULL,
		universe    TEXT NOT NULL,
		mode        TEXT NOT NULL,
		file_count  INTEGER NOT NULL,
		total_bytes INTEGER NOT NULL,
		seq         INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS generation_files (
		gen_id      TEXT NOT NULL REFERENCES generations(gen_id) ON DELETE CASCADE,
		artifact_id TEXT NOT NULL REFERENCES artifacts(artifact_id),
		PRIMARY KEY (gen_id, artifact_id)
	) WITHOUT ROWID`,
	`CREATE INDEX IF NOT EXISTS generation_files_artifact ON generation_files(artifact_id)`,
	// Reader pins: independent of database transactions.
	`CREATE TABLE IF NOT EXISTS pins (
		pin_id     TEXT PRIMARY KEY,
		gen_id     TEXT NOT NULL,
		holder     TEXT NOT NULL,
		expires_ns INTEGER NOT NULL DEFAULT 0,
		state      TEXT NOT NULL DEFAULT 'active'
	)`,
	`CREATE INDEX IF NOT EXISTS pins_gen ON pins(gen_id)`,
	`CREATE TABLE IF NOT EXISTS receipts (
		receipt_id  TEXT PRIMARY KEY,
		snapshot_id TEXT NOT NULL,
		policy_id   TEXT NOT NULL,
		observed_at TEXT NOT NULL,
		body        BLOB NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS receipts_snapshot ON receipts(snapshot_id)`,
	`CREATE TABLE IF NOT EXISTS receipt_blobs (
		sha256     TEXT NOT NULL,
		receipt_id TEXT NOT NULL REFERENCES receipts(receipt_id) ON DELETE CASCADE,
		PRIMARY KEY (sha256, receipt_id)
	) WITHOUT ROWID`,
	`CREATE INDEX IF NOT EXISTS receipt_blobs_receipt ON receipt_blobs(receipt_id)`,
	// Delivery ledger: every dossier and expansion shown under a receipt.
	`CREATE TABLE IF NOT EXISTS deliveries (
		delivery_id TEXT PRIMARY KEY,
		receipt_id  TEXT NOT NULL,
		kind        TEXT NOT NULL,
		observed_at TEXT NOT NULL,
		body        BLOB NOT NULL,
		count       INTEGER NOT NULL DEFAULT 1,
		seq         INTEGER NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS deliveries_receipt ON deliveries(receipt_id, seq)`,
	`CREATE TABLE IF NOT EXISTS delivery_blobs (
		sha256      TEXT NOT NULL,
		delivery_id TEXT NOT NULL REFERENCES deliveries(delivery_id) ON DELETE CASCADE,
		PRIMARY KEY (sha256, delivery_id)
	) WITHOUT ROWID`,
	`CREATE INDEX IF NOT EXISTS delivery_blobs_delivery ON delivery_blobs(delivery_id)`,
	// Revocations are stored as tombstones H(domain, blob) so a purge can
	// remove the blob hash itself while revocation stays durable.
	`CREATE TABLE IF NOT EXISTS revoked_blobs (
		tomb       TEXT PRIMARY KEY,
		revoked_at TEXT NOT NULL
	) WITHOUT ROWID`,
	`CREATE TABLE IF NOT EXISTS revoked_receipts (
		tomb TEXT PRIMARY KEY
	) WITHOUT ROWID`,
	// Generations revoked but not yet purged: unreadable and unpinnable.
	`CREATE TABLE IF NOT EXISTS revoked_generations (
		gen_id TEXT PRIMARY KEY
	) WITHOUT ROWID`,
}

func (s *Store) migrate(ctx context.Context) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		var v int
		if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
			return err
		}
		if v > SchemaVersion {
			return fmt.Errorf("store: schema version %d is newer than supported %d", v, SchemaVersion)
		}
		if v == SchemaVersion {
			return nil
		}
		for _, q := range schemaV2 {
			if _, err := tx.ExecContext(ctx, q); err != nil {
				return err
			}
		}
		if v == 1 {
			if err := migrateV1(ctx, tx); err != nil {
				return fmt.Errorf("store: migrating schema 1: %w", err)
			}
		}
		_, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", SchemaVersion))
		return err
	})
}

// migrateV1 moves schema-1 manifests into generations/artifacts and
// revoked blob hashes into tombstones.
func migrateV1(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT f.snapshot_id, f.path, f.sha256, f.size, f.skipped FROM snapshot_files f ORDER BY f.snapshot_id, f.path`)
	if err != nil {
		return err
	}
	type r struct {
		gen string
		f   FileRow
	}
	var all []r
	for rows.Next() {
		var x r
		if err := rows.Scan(&x.gen, &x.f.Path, &x.f.SHA256, &x.f.Size, &x.f.Skipped); err != nil {
			rows.Close()
			return err
		}
		all = append(all, x)
	}
	rows.Close()
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO generations
		SELECT snapshot_id, policy_id, universe, mode, file_count, total_bytes, rowid FROM snapshots`); err != nil {
		return err
	}
	for _, x := range all {
		id := ArtifactID(x.f)
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO artifacts VALUES (?,?,?,?,?)`, id, x.f.Path, x.f.SHA256, x.f.Size, x.f.Skipped); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO generation_files VALUES (?,?)`, x.gen, id); err != nil {
			return err
		}
	}
	rv, err := tx.QueryContext(ctx, `SELECT sha256, revoked_at FROM revoked`)
	if err != nil {
		return err
	}
	var revs [][2]string
	for rv.Next() {
		var a, b string
		if err := rv.Scan(&a, &b); err != nil {
			rv.Close()
			return err
		}
		revs = append(revs, [2]string{a, b})
	}
	rv.Close()
	for _, x := range revs {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO revoked_blobs VALUES (?,?)`, BlobTombstone(x[0]), x[1]); err != nil {
			return err
		}
	}
	for _, q := range []string{`DROP TABLE revoked`, `DROP TABLE snapshot_files`, `DROP TABLE snapshots`} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

// write runs fn in a short IMMEDIATE transaction under the writer lock.
func (s *Store) write(ctx context.Context, fn func(*sql.Tx) error) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// PutReceipt durably stores a receipt body with its blob reverse index.
// It returns only after commit (durable publication before the receipt is
// acknowledged to the caller).
func (s *Store) PutReceipt(ctx context.Context, id, snapshotID, policyID, observedAt string, body []byte, blobs []string) error {
	s.gate.RLock()
	defer s.gate.RUnlock()
	return s.write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO receipts VALUES (?,?,?,?,?)`,
			id, snapshotID, policyID, observedAt, body); err != nil {
			return err
		}
		tombs, err := tombstones(ctx, tx)
		if err != nil {
			return err
		}
		for _, b := range blobs {
			b = unrevoked(tombs, FileRow{SHA256: b}).SHA256
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO receipt_blobs VALUES (?,?)`, b, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// ErrNotFound is returned for unknown ids.
var ErrNotFound = errors.New("store: not found")

// Receipt loads a receipt body.
func (s *Store) Receipt(ctx context.Context, id string) ([]byte, error) {
	s.gate.RLock()
	defer s.gate.RUnlock()
	var body []byte
	err := s.db.QueryRowContext(ctx, `SELECT body FROM receipts WHERE receipt_id = ?`, id).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return body, err
}

// ResolveReceiptPrefix expands a unique receipt id prefix (>= 8 chars).
func (s *Store) ResolveReceiptPrefix(ctx context.Context, prefix string) (string, error) {
	s.gate.RLock()
	defer s.gate.RUnlock()
	if len(prefix) < 8 || strings.Trim(prefix, "abcdefghijklmnopqrstuvwxyz0123456789_") != "" {
		return "", ErrNotFound
	}
	rows, err := s.db.QueryContext(ctx, `SELECT receipt_id FROM receipts WHERE substr(receipt_id, 1, ?) = ? ORDER BY receipt_id LIMIT 2`, len(prefix), prefix)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
		ids = append(ids, id)
	}
	if len(ids) != 1 {
		return "", ErrNotFound
	}
	return ids[0], rows.Err()
}
