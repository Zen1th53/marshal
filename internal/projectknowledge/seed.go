package projectknowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"sort"
)

// seedCandidateLimit bounds how many sibling indexes a fresh worktree index
// inspects before it falls back to a full build.
const seedCandidateLimit = 8

// seedFromSibling starts a fresh task worktree index from the rows of an
// existing index of the same repository: the owner's own index or another
// lane's. Every lane of a run is a new worktree at (nearly) the same commit,
// so without a seed each one re-parses and re-inserts the whole repository.
//
// Seeded rows are derived metadata keyed by path and content hash. Refresh
// still scans and privacy-checks every current file, reuses a seeded row only
// when its path and hash match the current bytes, re-parses changed files and
// deletes rows for paths that are gone, exactly as for its own earlier rows.
// Any failure leaves the index empty and the ordinary full build runs.
func (i *Index) seedFromSibling(ctx context.Context, db *sql.DB, head string) bool {
	if i.seedRoot == "" {
		return false
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return false
	}
	defer conn.Close()
	var bound int
	if conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM metadata").Scan(&bound) != nil || bound != 0 {
		return false
	}
	donor := i.seedDonor(ctx, head)
	if donor == "" {
		return false
	}
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(donor), RawQuery: "mode=ro"}
	if _, err := conn.ExecContext(ctx, "ATTACH DATABASE ? AS seed", uri.String()); err != nil {
		return false
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), "DETACH DATABASE seed")
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return false
	}
	defer tx.Rollback()
	// The rows copied must belong to the snapshot read in this transaction.
	var data string
	if tx.QueryRowContext(ctx, "SELECT snapshot FROM seed.metadata WHERE id=1").Scan(&data) != nil || !compatibleSnapshot(data) {
		return false
	}
	if _, err = tx.ExecContext(ctx, indexTables); err != nil {
		return false
	}
	var present int
	if tx.QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM files)+(SELECT COUNT(*) FROM entries)").Scan(&present) != nil || present != 0 {
		return false
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO files(id,path,hash,package,metadata) SELECT id,path,hash,package,metadata FROM seed.files"); err != nil {
		return false
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO entries(file_id,line,name,kind,receiver,target) SELECT file_id,line,name,kind,receiver,target FROM seed.entries"); err != nil {
		return false
	}
	return tx.Commit() == nil
}

func compatibleSnapshot(data string) bool {
	var snapshot Snapshot
	return json.Unmarshal([]byte(data), &snapshot) == nil && snapshot.Version == Version && snapshot.PolicyDigest == policyDigest
}

// seedDonor picks a compatible sibling index, preferring one built at head.
// Only databases inside this owner's private knowledge directory that pass
// the same state checks as the index itself are considered.
func (i *Index) seedDonor(ctx context.Context, head string) string {
	candidates := []string{filepath.Join(i.seedRoot, "index-v1.db")}
	type sibling struct {
		path string
		mod  int64
	}
	var siblings []sibling
	entries, _ := os.ReadDir(filepath.Join(i.seedRoot, "worktrees"))
	for _, entry := range entries {
		path := filepath.Join(i.seedRoot, "worktrees", entry.Name(), "index-v1.db")
		if !entry.IsDir() || path == i.database {
			continue
		}
		if st, err := os.Lstat(path); err == nil && st.Mode().IsRegular() {
			siblings = append(siblings, sibling{path, st.ModTime().UnixNano()})
		}
	}
	sort.Slice(siblings, func(a, b int) bool { return siblings[a].mod > siblings[b].mod })
	for _, s := range siblings {
		candidates = append(candidates, s.path)
	}
	if len(candidates) > seedCandidateLimit {
		candidates = candidates[:seedCandidateLimit]
	}
	fallback := ""
	for _, path := range candidates {
		snapshot, ok := i.donorSnapshot(ctx, path)
		if !ok {
			continue
		}
		if snapshot.Head == head {
			return path
		}
		if fallback == "" {
			fallback = path
		}
	}
	return fallback
}

func (i *Index) donorSnapshot(ctx context.Context, path string) (Snapshot, bool) {
	if path == i.database {
		return Snapshot{}, false
	}
	donor := &Index{root: i.root, state: i.state, database: path}
	if donor.safeState() != nil {
		return Snapshot{}, false
	}
	if st, err := os.Lstat(path); err != nil || !st.Mode().IsRegular() {
		return Snapshot{}, false
	}
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(1000)"}}.Encode()}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return Snapshot{}, false
	}
	defer db.Close()
	var data string
	if db.QueryRowContext(ctx, "SELECT snapshot FROM metadata WHERE id=1").Scan(&data) != nil || !compatibleSnapshot(data) {
		return Snapshot{}, false
	}
	var snapshot Snapshot
	_ = json.Unmarshal([]byte(data), &snapshot)
	return snapshot, true
}
