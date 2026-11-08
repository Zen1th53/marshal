package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// FileRow is one manifest entry (a per-file artifact).
type FileRow struct {
	Path    string
	SHA256  string
	Size    int64
	Skipped string
}

// ArtifactID is the content address of a per-file artifact.
func ArtifactID(f FileRow) string {
	h := sha256.New()
	h.Write([]byte("cec/artifact/v1\x00" + f.Path + "\x00" + f.SHA256 + "\x00" + f.Skipped + "\x00"))
	var n [8]byte
	for i := 0; i < 8; i++ {
		n[i] = byte(uint64(f.Size) >> (8 * i))
	}
	h.Write(n[:])
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// Generation describes a published, immutable evidence generation.
type Generation struct {
	ID         string
	PolicyID   string
	Universe   string
	Mode       string
	FileCount  int
	TotalBytes int64
	Seq        int64
}

// artifactChunk bounds each artifact transaction (short transactions).
const artifactChunk = 2000

// PublishGeneration publishes an immutable manifest. Artifacts are written
// first in short transactions (reusing rows shared with other
// generations); the manifest (generation row plus all references) is then
// published in one atomic transaction, and the call returns only after
// that commit. Publishing an existing generation id is a no-op.
//
// A crash before the manifest commit leaves only unreferenced artifacts,
// which GC removes; a reader never sees a partial manifest.
func (s *Store) PublishGeneration(ctx context.Context, g Generation, files []FileRow) error {
	s.gate.RLock()
	defer s.gate.RUnlock()
	if ok, err := s.generationExists(ctx, g.ID); err != nil || ok {
		return err
	}
	s.publishing.Add(1)
	defer s.publishing.Add(-1)
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		last := attempt == 2
		if !last {
			if err = s.putArtifacts(ctx, files); err != nil {
				return err
			}
		}
		// The last attempt writes the artifacts inside the manifest
		// transaction, so a concurrent GC (e.g. from another process)
		// cannot collect them in between.
		if err = s.publishManifest(ctx, g, files, last); err == nil || !isConstraint(err) {
			return err
		}
	}
	return err
}

func isConstraint(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "constraint")
}

func (s *Store) generationExists(ctx context.Context, id string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM generations WHERE gen_id = ?", id).Scan(&n)
	return n > 0, err
}

// tombstones loads the revocation tombstones inside a write transaction
// (serialized with Revoke, so no revocation can slip in before commit).
func tombstones(ctx context.Context, tx *sql.Tx) (map[string]bool, error) {
	ts, err := queryStrings(ctx, tx, `SELECT tomb FROM revoked_blobs`)
	if err != nil {
		return nil, err
	}
	m := make(map[string]bool, len(ts))
	for _, t := range ts {
		m[t] = true
	}
	return m, nil
}

// unrevoked replaces the hash of a revoked blob by its tombstone, so a
// revoked hash never re-enters the store after its purge (a later capture
// may still observe the same bytes).
func unrevoked(tombs map[string]bool, f FileRow) FileRow {
	if len(tombs) > 0 && f.SHA256 != "" && tombs[BlobTombstone(f.SHA256)] {
		f.SHA256 = BlobTombstone(f.SHA256)
	}
	return f
}

func insertArtifacts(ctx context.Context, tx *sql.Tx, tombs map[string]bool, files []FileRow) ([]string, error) {
	st, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO artifacts VALUES (?,?,?,?,?)`)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	ids := make([]string, len(files))
	for i, f := range files {
		f = unrevoked(tombs, f)
		ids[i] = ArtifactID(f)
		if _, err := st.ExecContext(ctx, ids[i], f.Path, f.SHA256, f.Size, f.Skipped); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

func (s *Store) putArtifacts(ctx context.Context, files []FileRow) error {
	for lo := 0; lo < len(files); lo += artifactChunk {
		hi := min(lo+artifactChunk, len(files))
		err := s.write(ctx, func(tx *sql.Tx) error {
			tombs, err := tombstones(ctx, tx)
			if err != nil {
				return err
			}
			_, err = insertArtifacts(ctx, tx, tombs, files[lo:hi])
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) publishManifest(ctx context.Context, g Generation, files []FileRow, withArtifacts bool) error {
	var total int64
	for _, f := range files {
		total += f.Size
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO generations
			VALUES (?,?,?,?,?,?, (SELECT coalesce(max(seq), 0) + 1 FROM generations))`,
			g.ID, g.PolicyID, g.Universe, g.Mode, len(files), total)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil // published concurrently
		}
		tombs, err := tombstones(ctx, tx)
		if err != nil {
			return err
		}
		ids := make([]string, len(files))
		var revoked []FileRow
		for i, f := range files {
			if u := unrevoked(tombs, f); u != f || withArtifacts {
				revoked = append(revoked, f)
			}
			ids[i] = ArtifactID(unrevoked(tombs, f))
		}
		// Blobs revoked after the artifact step (or every artifact, when
		// asked): write them now, inside the manifest transaction.
		if _, err := insertArtifacts(ctx, tx, tombs, revoked); err != nil {
			return err
		}
		st, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO generation_files VALUES (?,?)`)
		if err != nil {
			return err
		}
		defer st.Close()
		for _, id := range ids {
			if _, err := st.ExecContext(ctx, g.ID, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// PutSnapshot publishes a snapshot manifest as a generation (idempotent).
func (s *Store) PutSnapshot(ctx context.Context, id, policyID, universe, mode string, files []FileRow) error {
	return s.PublishGeneration(ctx, Generation{ID: id, PolicyID: policyID, Universe: universe, Mode: mode}, files)
}

// GenerationInfo returns a published generation.
func (s *Store) GenerationInfo(ctx context.Context, id string) (Generation, error) {
	s.gate.RLock()
	defer s.gate.RUnlock()
	var g Generation
	err := s.db.QueryRowContext(ctx, `SELECT gen_id, policy_id, universe, mode, file_count, total_bytes, seq FROM generations WHERE gen_id = ?`, id).
		Scan(&g.ID, &g.PolicyID, &g.Universe, &g.Mode, &g.FileCount, &g.TotalBytes, &g.Seq)
	if errors.Is(err, sql.ErrNoRows) {
		return g, ErrNotFound
	}
	return g, err
}

// SnapshotFiles loads a published manifest (sorted by path). A missing or
// revoked generation yields no rows.
func (s *Store) SnapshotFiles(ctx context.Context, id string) ([]FileRow, error) {
	s.gate.RLock()
	defer s.gate.RUnlock()
	return scanFiles(s.db.QueryContext(ctx, `SELECT a.path, a.sha256, a.size, a.skipped
		FROM generation_files gf JOIN artifacts a ON a.artifact_id = gf.artifact_id
		WHERE gf.gen_id = ? AND gf.gen_id NOT IN (SELECT gen_id FROM revoked_generations) ORDER BY a.path`, id))
}

func scanFiles(rows *sql.Rows, err error) ([]FileRow, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FileRow
	for rows.Next() {
		var f FileRow
		if err := rows.Scan(&f.Path, &f.SHA256, &f.Size, &f.Skipped); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Pin errors.
var (
	// ErrPinRevoked is returned to holders of a pin whose generation was
	// purged by a revocation (revocation overrides pins).
	ErrPinRevoked = errors.New("store: pin revoked: generation contained revoked evidence")
)

// Pin takes a reader pin on a published generation. The pin is a row, not
// an open transaction: the holder reads the generation through any number
// of short transactions until it calls Unpin. GC never collects a pinned
// generation; revocation does, and marks the pin revoked. ttl > 0 lets GC
// drop the pin of a holder that died without unpinning.
func (s *Store) Pin(ctx context.Context, genID, holder string, ttl time.Duration) (string, error) {
	s.gate.RLock()
	defer s.gate.RUnlock()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	id := "pin_" + hex.EncodeToString(b[:])
	var exp int64
	if ttl > 0 {
		exp = time.Now().Add(ttl).UnixNano()
	}
	err := s.write(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM generations WHERE gen_id = ?`, genID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM revoked_generations WHERE gen_id = ?`, genID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrPinRevoked
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO pins VALUES (?,?,?,?, 'active')`, id, genID, holder, exp)
		return err
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

// Unpin releases a pin (idempotent).
func (s *Store) Unpin(ctx context.Context, pinID string) error {
	s.gate.RLock()
	defer s.gate.RUnlock()
	return s.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM pins WHERE pin_id = ?`, pinID)
		return err
	})
}

// PinnedFiles reads the manifest of a pinned generation in one statement
// (one short read transaction). It fails with ErrPinRevoked once a
// revocation purged the generation, and with ErrNotFound for an unknown or
// released pin. A successful read is always the complete manifest.
func (s *Store) PinnedFiles(ctx context.Context, pinID string) (Generation, []FileRow, error) {
	s.gate.RLock()
	defer s.gate.RUnlock()
	rows, err := s.db.QueryContext(ctx, `SELECT g.gen_id, g.policy_id, g.universe, g.mode, g.file_count, g.total_bytes, g.seq,
			coalesce(a.path, ''), coalesce(a.sha256, ''), coalesce(a.size, 0), coalesce(a.skipped, '')
		FROM pins p JOIN generations g ON g.gen_id = p.gen_id
		LEFT JOIN generation_files gf ON gf.gen_id = g.gen_id
		LEFT JOIN artifacts a ON a.artifact_id = gf.artifact_id
		WHERE p.pin_id = ? AND p.state = 'active' AND g.gen_id NOT IN (SELECT gen_id FROM revoked_generations)
		ORDER BY a.path`, pinID)
	if err != nil {
		return Generation{}, nil, err
	}
	var g Generation
	var files []FileRow
	found := false
	for rows.Next() {
		var f FileRow
		if err := rows.Scan(&g.ID, &g.PolicyID, &g.Universe, &g.Mode, &g.FileCount, &g.TotalBytes, &g.Seq, &f.Path, &f.SHA256, &f.Size, &f.Skipped); err != nil {
			rows.Close()
			return Generation{}, nil, err
		}
		found = true
		if f.Path != "" {
			files = append(files, f)
		}
	}
	if err := rows.Close(); err != nil {
		return Generation{}, nil, err
	}
	if err := rows.Err(); err != nil {
		return Generation{}, nil, err
	}
	if found {
		return g, files, nil
	}
	var state string
	err = s.db.QueryRowContext(ctx, `SELECT state FROM pins WHERE pin_id = ?`, pinID).Scan(&state)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Generation{}, nil, ErrNotFound
	case err != nil:
		return Generation{}, nil, err
	case state == "revoked":
		return Generation{}, nil, ErrPinRevoked
	}
	return Generation{}, nil, ErrNotFound
}

// GCReport summarizes a collection.
type GCReport struct {
	Generations  int `json:"generations"`
	Artifacts    int `json:"artifacts"`
	ExpiredPins  int `json:"expired_pins"`
	RetainedPins int `json:"retained_pinned"`
}

// GC removes generations that are neither pinned, nor referenced by a
// stored receipt, nor among the keep most recently published ones, then
// every artifact no generation references (including orphans left by a
// crashed publication; skipped while this process is publishing). Expired pins are dropped first. GC runs in one
// short write transaction and therefore never interleaves with a
// manifest publication.
func (s *Store) GC(ctx context.Context, keep int) (GCReport, error) {
	s.gate.RLock()
	defer s.gate.RUnlock()
	var r GCReport
	err := s.write(ctx, func(tx *sql.Tx) error {
		r = GCReport{}
		res, err := tx.ExecContext(ctx, `DELETE FROM pins WHERE expires_ns > 0 AND expires_ns < ?`, time.Now().UnixNano())
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		r.ExpiredPins = int(n)
		if err := tx.QueryRowContext(ctx, `SELECT count(DISTINCT gen_id) FROM pins WHERE state = 'active'`).Scan(&r.RetainedPins); err != nil {
			return err
		}
		res, err = tx.ExecContext(ctx, `DELETE FROM generations WHERE
			gen_id NOT IN (SELECT gen_id FROM pins WHERE state = 'active')
			AND gen_id NOT IN (SELECT snapshot_id FROM receipts)
			AND gen_id NOT IN (SELECT gen_id FROM generations ORDER BY seq DESC LIMIT ?)`, keep)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		r.Generations = int(n)
		if s.publishing.Load() > 0 {
			return nil // orphans may belong to an in-flight publication
		}
		res, err = tx.ExecContext(ctx, `DELETE FROM artifacts WHERE artifact_id NOT IN (SELECT artifact_id FROM generation_files)`)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		r.Artifacts = int(n)
		return nil
	})
	return r, err
}

// Stats reports row counts and on-disk size (db + WAL + shm).
type Stats struct {
	Generations, Artifacts, ManifestRows, Receipts, Deliveries, Pins int
	Bytes                                                            int64
}

// Stats returns store statistics.
func (s *Store) Stats(ctx context.Context) (Stats, error) {
	s.gate.RLock()
	defer s.gate.RUnlock()
	var st Stats
	for _, q := range []struct {
		sql string
		dst *int
	}{
		{"SELECT count(*) FROM generations", &st.Generations},
		{"SELECT count(*) FROM artifacts", &st.Artifacts},
		{"SELECT count(*) FROM generation_files", &st.ManifestRows},
		{"SELECT count(*) FROM receipts", &st.Receipts},
		{"SELECT count(*) FROM deliveries", &st.Deliveries},
		{"SELECT count(*) FROM pins", &st.Pins},
	} {
		if err := s.db.QueryRowContext(ctx, q.sql).Scan(q.dst); err != nil {
			return st, err
		}
	}
	st.Bytes = s.DiskBytes()
	return st, nil
}

// DiskBytes is the size of the database, WAL and shared-memory files.
func (s *Store) DiskBytes() int64 {
	var n int64
	for _, f := range s.Files() {
		if fi, err := statFile(f); err == nil {
			n += fi
		}
	}
	return n
}
