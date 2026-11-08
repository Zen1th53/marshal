package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
)

// BlobTombstone is the durable revocation marker of a blob: a one-way,
// domain-separated hash of the blob hash. It lets revocation outlive the
// purge of the blob hash itself; only someone who already knows the blob
// hash can match it.
func BlobTombstone(sha string) string {
	h := sha256.Sum256([]byte("cec/revoked-blob/v1\x00" + sha))
	return "tomb_" + hex.EncodeToString(h[:16])
}

// ReceiptTombstone marks a receipt purged by a revocation.
func ReceiptTombstone(id string) string {
	h := sha256.Sum256([]byte("cec/revoked-receipt/v1\x00" + id))
	return "tomb_" + hex.EncodeToString(h[:16])
}

// Revocation is the durable result of Revoke.
type Revocation struct {
	Receipts    []string // receipts whose evidence used the blob
	Deliveries  int      // ledger entries that delivered the blob
	Generations []string // generations whose manifest contains the blob
	PinsRevoked int      // active pins overridden
}

// Revoke durably records a revocation of blob (one transaction) and marks
// every pin on a generation containing it revoked: revocation overrides
// pins. It returns what the purge will remove. Revoke never deletes data;
// Purge does.
func (s *Store) Revoke(ctx context.Context, sha, at string) (Revocation, error) {
	s.gate.RLock()
	defer s.gate.RUnlock()
	var rv Revocation
	err := s.write(ctx, func(tx *sql.Tx) error {
		rv = Revocation{}
		// Older stores indexed expansion dependencies only by delivery.
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO receipt_blobs SELECT db.sha256, d.receipt_id FROM delivery_blobs db JOIN deliveries d USING(delivery_id)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO revoked_blobs VALUES (?,?)`, BlobTombstone(sha), at); err != nil {
			return err
		}
		var err error
		if rv.Receipts, err = queryStrings(ctx, tx, `SELECT receipt_id FROM receipt_blobs WHERE sha256 = ? ORDER BY receipt_id`, sha); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM delivery_blobs WHERE sha256 = ?`, sha).Scan(&rv.Deliveries); err != nil {
			return err
		}
		if rv.Generations, err = queryStrings(ctx, tx, `SELECT DISTINCT gf.gen_id FROM generation_files gf
			JOIN artifacts a ON a.artifact_id = gf.artifact_id WHERE a.sha256 = ? ORDER BY gf.gen_id`, sha); err != nil {
			return err
		}
		for _, g := range rv.Generations {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO revoked_generations VALUES (?)`, g); err != nil {
				return err
			}
		}
		res, err := tx.ExecContext(ctx, `UPDATE pins SET state = 'revoked' WHERE state = 'active' AND gen_id IN
			(SELECT gen_id FROM revoked_generations)`)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		rv.PinsRevoked = int(n)
		return nil
	})
	return rv, err
}

func queryStrings(ctx context.Context, tx *sql.Tx, q string, args ...any) ([]string, error) {
	return scanStrings(tx.QueryContext(ctx, q, args...))
}

// RevokedBlobs reports which of the given blobs are revoked.
func (s *Store) RevokedBlobs(ctx context.Context, shas []string) (map[string]bool, error) {
	s.gate.RLock()
	defer s.gate.RUnlock()
	out := map[string]bool{}
	for _, h := range shas {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM revoked_blobs WHERE tomb = ?`, BlobTombstone(h)).Scan(&n); err != nil {
			return nil, err
		}
		if n > 0 {
			out[h] = true
		}
	}
	return out, nil
}

// Tombstones returns the set of revocation tombstones (see BlobTombstone).
func (s *Store) Tombstones(ctx context.Context) (map[string]bool, error) {
	s.gate.RLock()
	defer s.gate.RUnlock()
	ts, err := scanStrings(s.db.QueryContext(ctx, `SELECT tomb FROM revoked_blobs`))
	if err != nil {
		return nil, err
	}
	m := make(map[string]bool, len(ts))
	for _, t := range ts {
		m[t] = true
	}
	return m, nil
}

func scanStrings(rows *sql.Rows, err error) ([]string, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Revoked is RevokedBlobs (kept for callers of the schema-1 API).
func (s *Store) Revoked(ctx context.Context, shas []string) (map[string]bool, error) {
	return s.RevokedBlobs(ctx, shas)
}

// ReceiptRevoked reports whether a receipt id was purged by a revocation.
func (s *Store) ReceiptRevoked(ctx context.Context, id string) (bool, error) {
	s.gate.RLock()
	defer s.gate.RUnlock()
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM revoked_receipts WHERE tomb = ?`, ReceiptTombstone(id)).Scan(&n)
	return n > 0, err
}

// PurgeReport describes a physical purge.
type PurgeReport struct {
	Receipts     int  `json:"receipts"`
	Deliveries   int  `json:"deliveries"`
	Generations  int  `json:"generations"`
	Artifacts    int  `json:"artifacts"`
	Vacuumed     bool `json:"vacuumed"`
	Checkpointed bool `json:"wal_truncated"`
}

// Purge physically removes everything derived from a revoked blob:
// receipts and ledger entries that delivered it (replaced by receipt
// tombstones), generations whose manifest contains it, and its artifacts.
// It then checkpoints and truncates the WAL, VACUUMs (secure_delete is on
// and temporary storage is in memory) and truncates the WAL again, so the
// bytes are gone from the database, WAL and shared-memory files. In-process
// readers and writers are drained for the duration. Purge requires a prior
// Revoke of the same blob.
func (s *Store) Purge(ctx context.Context, sha string) (PurgeReport, error) {
	s.gate.Lock()
	defer s.gate.Unlock()
	var r PurgeReport
	err := s.write(ctx, func(tx *sql.Tx) error {
		r = PurgeReport{}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM revoked_blobs WHERE tomb = ?`, BlobTombstone(sha)).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return errors.New("store: purge without revocation")
		}
		receipts, err := queryStrings(ctx, tx, `SELECT receipt_id FROM receipt_blobs WHERE sha256 = ?`, sha)
		if err != nil {
			return err
		}
		for _, id := range receipts {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO revoked_receipts VALUES (?)`, ReceiptTombstone(id)); err != nil {
				return err
			}
		}
		// Ledger entries under purged receipts or delivering the blob.
		res, err := tx.ExecContext(ctx, `DELETE FROM deliveries WHERE
			delivery_id IN (SELECT delivery_id FROM delivery_blobs WHERE sha256 = ?)
			OR receipt_id IN (SELECT receipt_id FROM receipt_blobs WHERE sha256 = ?)`, sha, sha)
		if err != nil {
			return err
		}
		c, _ := res.RowsAffected()
		r.Deliveries = int(c)
		res, err = tx.ExecContext(ctx, `DELETE FROM receipts WHERE receipt_id IN (SELECT receipt_id FROM receipt_blobs WHERE sha256 = ?)`, sha)
		if err != nil {
			return err
		}
		c, _ = res.RowsAffected()
		r.Receipts = int(c)
		// Revocation overrides pins: generations go even when pinned.
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO revoked_generations
			SELECT DISTINCT gf.gen_id FROM generation_files gf JOIN artifacts a ON a.artifact_id = gf.artifact_id WHERE a.sha256 = ?`, sha); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE pins SET state = 'revoked' WHERE state = 'active' AND gen_id IN
			(SELECT gen_id FROM revoked_generations)`); err != nil {
			return err
		}
		res, err = tx.ExecContext(ctx, `DELETE FROM generations WHERE gen_id IN (SELECT gen_id FROM revoked_generations)`)
		if err != nil {
			return err
		}
		c, _ = res.RowsAffected()
		r.Generations = int(c)
		if _, err := tx.ExecContext(ctx, `DELETE FROM revoked_generations`); err != nil {
			return err
		}
		res, err = tx.ExecContext(ctx, `DELETE FROM artifacts WHERE artifact_id NOT IN (SELECT artifact_id FROM generation_files)`)
		if err != nil {
			return err
		}
		c, _ = res.RowsAffected()
		r.Artifacts = int(c)
		// Rows of other tables that still name the blob (defensive: the
		// cascades above should have removed them).
		for _, q := range []string{`DELETE FROM receipt_blobs WHERE sha256 = ?`, `DELETE FROM delivery_blobs WHERE sha256 = ?`} {
			if _, err := tx.ExecContext(ctx, q, sha); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return r, err
	}
	if err := s.checkpoint(ctx); err != nil {
		return r, err
	}
	if _, err := s.db.ExecContext(ctx, "VACUUM"); err != nil {
		return r, fmt.Errorf("store: vacuum: %w", err)
	}
	r.Vacuumed = true
	if err := s.checkpoint(ctx); err != nil {
		return r, err
	}
	r.Checkpointed = true
	return r, nil
}

// checkpoint copies the WAL into the database and truncates it to zero
// bytes. It fails if another connection (e.g. another process) keeps a
// read transaction open.
func (s *Store) checkpoint(ctx context.Context) error {
	var busy, logFrames, done int
	if err := s.db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logFrames, &done); err != nil {
		return fmt.Errorf("store: checkpoint: %w", err)
	}
	if busy != 0 {
		return errors.New("store: checkpoint blocked by another reader; purge incomplete, retry forget")
	}
	return nil
}
