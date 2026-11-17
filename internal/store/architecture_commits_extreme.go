//go:build !governed

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/Zen1th53/marshal/internal/projectcontext"
)

func init() {
	registerCanonicalMigration(schemaExtremeArchitectureCommits, func(ctx context.Context, tx *sql.Tx) error {
		if err := requireAdoptableTables(ctx, tx, architectureCommitsDDL); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, architectureCommitsDDL)
		return err
	})
}

// Each row says which accepted project contract revision applied when a
// commit was made or moved: when the owner accepted it, when a task was
// merged and when the run was delivered. Rows are never changed.
const architectureCommitsDDL = `
CREATE TABLE IF NOT EXISTS extreme_architecture_commits (
 project_id TEXT NOT NULL,commit_sha TEXT NOT NULL,tree_sha TEXT NOT NULL,
 contract_id TEXT NOT NULL,revision INTEGER NOT NULL,digest TEXT NOT NULL,
 event TEXT NOT NULL CHECK(event IN ('accepted','merged','delivered')),
 task_id TEXT NOT NULL,recorded_at TEXT NOT NULL,
 PRIMARY KEY(project_id,commit_sha,event,contract_id,revision),
 FOREIGN KEY(project_id,contract_id,revision,digest)
 REFERENCES extreme_project_contract_revisions(project_id,contract_id,revision,digest)
);
CREATE INDEX IF NOT EXISTS extreme_architecture_commits_by_time ON extreme_architecture_commits(project_id,recorded_at);
`

// ArchitectureCommit binds one commit to the accepted contract revision.
type ArchitectureCommit struct {
	Commit     string                     `json:"commit"`
	Tree       string                     `json:"tree"`
	Contract   projectcontext.ContractRef `json:"contract"`
	Event      string                     `json:"event"`
	Task       string                     `json:"task_id,omitempty"`
	RecordedAt string                     `json:"recorded_at"`
}

var gitObjectID = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// RecordArchitectureCommit stores one binding; recording the same binding
// again changes nothing.
func (s *Store) RecordArchitectureCommit(ctx context.Context, project projectcontext.ProjectBinding, b ArchitectureCommit) error {
	if !gitObjectID.MatchString(b.Commit) || !gitObjectID.MatchString(b.Tree) || b.Contract.ID == "" || b.Contract.Revision < 1 || b.Contract.Digest == "" {
		return projectcontext.ErrInvalid
	}
	switch b.Event {
	case "accepted", "merged", "delivered":
	default:
		return projectcontext.ErrInvalid
	}
	if b.RecordedAt == "" {
		b.RecordedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO extreme_architecture_commits(project_id,commit_sha,tree_sha,contract_id,revision,digest,event,task_id,recorded_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		project.CanonicalID, b.Commit, b.Tree, b.Contract.ID, b.Contract.Revision, b.Contract.Digest, b.Event, b.Task, b.RecordedAt)
	return err
}

// ArchitectureCommitsFor returns the bindings of the given commits, keyed by
// commit, each list oldest first.
func (s *Store) ArchitectureCommitsFor(ctx context.Context, project projectcontext.ProjectBinding, commits []string) (map[string][]ArchitectureCommit, error) {
	found := map[string][]ArchitectureCommit{}
	for start := 0; start < len(commits); start += 400 {
		chunk := commits[start:min(start+400, len(commits))]
		args := []any{project.CanonicalID}
		marks := ""
		for i, c := range chunk {
			if i > 0 {
				marks += ","
			}
			marks += "?"
			args = append(args, c)
		}
		rows, err := s.db.QueryContext(ctx, `SELECT commit_sha,tree_sha,contract_id,revision,digest,event,task_id,recorded_at FROM extreme_architecture_commits WHERE project_id=? AND commit_sha IN (`+marks+`) ORDER BY recorded_at`, args...)
		if err != nil {
			return nil, err
		}
		list, err := scanArchitectureCommits(rows)
		if err != nil {
			return nil, err
		}
		for _, b := range list {
			found[b.Commit] = append(found[b.Commit], b)
		}
	}
	return found, nil
}

// ArchitectureCommitHistory returns the newest bindings first.
func (s *Store) ArchitectureCommitHistory(ctx context.Context, project projectcontext.ProjectBinding, limit int) ([]ArchitectureCommit, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT commit_sha,tree_sha,contract_id,revision,digest,event,task_id,recorded_at FROM extreme_architecture_commits WHERE project_id=? ORDER BY recorded_at DESC,commit_sha LIMIT ?`, project.CanonicalID, limit)
	if err != nil {
		return nil, err
	}
	return scanArchitectureCommits(rows)
}

func scanArchitectureCommits(rows *sql.Rows) ([]ArchitectureCommit, error) {
	defer rows.Close()
	var list []ArchitectureCommit
	for rows.Next() {
		var b ArchitectureCommit
		if err := rows.Scan(&b.Commit, &b.Tree, &b.Contract.ID, &b.Contract.Revision, &b.Contract.Digest, &b.Event, &b.Task, &b.RecordedAt); err != nil {
			return nil, err
		}
		list = append(list, b)
	}
	return list, rows.Err()
}

// ReadProjectContractRevision reads one stored contract revision exactly as
// it was accepted, checked against its digest.
func (s *Store) ReadProjectContractRevision(ctx context.Context, project projectcontext.ProjectBinding, ref projectcontext.ContractRef) (projectcontext.Contract, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT contract_json FROM extreme_project_contract_revisions WHERE project_id=? AND contract_id=? AND revision=? AND digest=?`, project.CanonicalID, ref.ID, ref.Revision, ref.Digest).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return projectcontext.Contract{}, projectcontext.ErrReviewPending
	}
	if err != nil {
		return projectcontext.Contract{}, err
	}
	var c projectcontext.Contract
	if err = json.Unmarshal([]byte(raw), &c); err != nil {
		return c, err
	}
	if digest, err := c.CanonicalDigest(); err != nil || digest != ref.Digest {
		return c, projectcontext.Changed("architecture_commits_extreme.go: revision digest")
	}
	return c, nil
}
