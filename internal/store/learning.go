package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/learning"
)

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(timeLayout)
}

// AppendMemoryCommit writes one Process 07 memory commit and every record it
// carries in a single transaction.
//
// The commit is append-only and digest-protected. Items are written through CAS
// on (item_id, version), so a concurrent learner cannot lose an invalidation or
// promote the same claim twice: a conflicting version aborts the whole commit
// rather than partially applying it.
func (s *Store) AppendMemoryCommit(ctx context.Context, c learning.Commit) error {
	if err := c.Verify(); err != nil {
		return err
	}
	if !c.Binding.Valid() {
		return fmt.Errorf("%w: entry binding", learning.ErrInvalid)
	}
	body, err := json.Marshal(c)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin memory commit: %w", err)
	}
	defer tx.Rollback()

	created := c.CreatedAt.UTC().Format(timeLayout)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO memory_commits(memory_commit_id,version,project_id,goal_id,plan_id,run_id,verification_id,verification_version,outcome,attestation_digest,evidence_bundle_digest,digest,commit_json,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	`, c.ID, c.Version, c.Binding.ProjectID, c.Binding.GoalID, c.Binding.PlanID, c.Binding.RunID,
		c.Binding.VerificationID, c.Binding.VerificationVersion, string(c.Binding.Outcome),
		c.Binding.AttestationDigest, c.Binding.EvidenceDigest, c.Digest, body, created); err != nil {
		return fmt.Errorf("append memory commit: %w", err)
	}

	for _, it := range c.Additions {
		if err := insertItem(ctx, tx, c.ID, it); err != nil {
			return err
		}
	}
	for _, it := range c.Revisions {
		if err := reviseItem(ctx, tx, c.ID, it); err != nil {
			return err
		}
	}
	for _, id := range c.Invalidations {
		if err := invalidateItem(ctx, tx, c.ID, id, c.CreatedAt); err != nil {
			return err
		}
	}
	for i, obs := range c.Observations {
		raw, err := json.Marshal(obs)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO routing_observations(observation_id,memory_commit_id,task_class,provider,provider_version,model,outcome,selected,cluster_id,observation_json,observed_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?)
		`, fmt.Sprintf("%s-obs-%d", c.ID, i), c.ID, obs.TaskClass, obs.Provider, obs.ProviderVersion, obs.Model,
			string(obs.Outcome), boolInt(obs.Selected), obs.ClusterID, raw,
			obs.Observed.UTC().Format(timeLayout)); err != nil {
			return fmt.Errorf("append routing observation: %w", err)
		}
	}
	for _, f := range c.Fingerprints {
		raw, err := json.Marshal(f)
		if err != nil {
			return err
		}
		// A fingerprint that recurs updates its occurrence count and freshness
		// rather than becoming a second, independently-counted record.
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO failure_fingerprints(fingerprint_id,signature,task_class,scope,project_id,occurrences,fingerprint_json,first_seen,last_seen,expires_at)
			VALUES(?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(fingerprint_id) DO UPDATE SET
				occurrences=occurrences+excluded.occurrences,
				fingerprint_json=excluded.fingerprint_json,
				last_seen=excluded.last_seen,
				expires_at=excluded.expires_at
		`, f.ID, f.Signature, f.TaskClass, string(f.Scope), f.ProjectID, f.Occurrences, raw,
			f.FirstSeen.UTC().Format(timeLayout), f.LastSeen.UTC().Format(timeLayout),
			nullableTime(f.ExpiresAt)); err != nil {
			return fmt.Errorf("append failure fingerprint: %w", err)
		}
	}
	for _, p := range c.Playbooks {
		if p.Active {
			return fmt.Errorf("%w: playbook self-activation", learning.ErrInvalid)
		}
		raw, err := json.Marshal(p)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO playbook_candidates(playbook_id,memory_commit_id,title,scope,project_id,active,candidate_json,created_at)
			VALUES(?,?,?,?,?,0,?,?)
		`, p.ID, c.ID, p.Title, string(p.Scope), p.ProjectID, raw, created); err != nil {
			return fmt.Errorf("append playbook candidate: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit memory commit: %w", err)
	}
	return nil
}

func insertItem(ctx context.Context, tx *sql.Tx, commitID string, it learning.Item) error {
	raw, err := json.Marshal(it)
	if err != nil {
		return err
	}
	d, err := learning.ItemDigest(it)
	if err != nil {
		return err
	}
	recorded := it.RecordedAt.UTC().Format(timeLayout)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO memory_items(item_id,version,scope,project_id,state,critical,memory_commit_id,digest,item_json,recorded_at,expires_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)
	`, it.ID, it.Version, string(it.Scope), it.ProjectID, string(it.State), boolInt(it.Critical),
		commitID, d, raw, recorded, nullableTime(it.ExpiresAt)); err != nil {
		return fmt.Errorf("insert memory item: %w", err)
	}
	return writeItemGraph(ctx, tx, commitID, it, d, raw, recorded)
}

// reviseItem applies a new version through CAS. The prior version stays in
// memory_item_revisions, so history is auditable and nothing is overwritten in
// place.
func reviseItem(ctx context.Context, tx *sql.Tx, commitID string, it learning.Item) error {
	raw, err := json.Marshal(it)
	if err != nil {
		return err
	}
	d, err := learning.ItemDigest(it)
	if err != nil {
		return err
	}
	recorded := it.RecordedAt.UTC().Format(timeLayout)
	res, err := tx.ExecContext(ctx, `
		UPDATE memory_items
		SET version=?,scope=?,project_id=?,state=?,critical=?,memory_commit_id=?,digest=?,item_json=?,recorded_at=?,expires_at=?
		WHERE item_id=? AND version=?
	`, it.Version, string(it.Scope), it.ProjectID, string(it.State), boolInt(it.Critical), commitID, d, raw,
		recorded, nullableTime(it.ExpiresAt), it.ID, it.Version-1)
	if err != nil {
		return fmt.Errorf("revise memory item: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return learning.ErrConflict
	}
	return writeItemGraph(ctx, tx, commitID, it, d, raw, recorded)
}

// invalidateItem marks a stored item invalidated through CAS on its current
// version, so a concurrent promotion cannot silently drop the invalidation.
func invalidateItem(ctx context.Context, tx *sql.Tx, commitID, itemID string, now time.Time) error {
	var version int64
	var body []byte
	err := tx.QueryRowContext(ctx, `SELECT version,item_json FROM memory_items WHERE item_id=?`, itemID).Scan(&version, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: invalidation target %s", learning.ErrInvalid, itemID)
	}
	if err != nil {
		return err
	}
	var it learning.Item
	if err := json.Unmarshal(body, &it); err != nil {
		return fmt.Errorf("decode memory item: %w", err)
	}
	it.State = learning.InvalidatedState()
	it.Version = version + 1
	it.RecordedAt = now
	return reviseItem(ctx, tx, commitID, it)
}

func writeItemGraph(ctx context.Context, tx *sql.Tx, commitID string, it learning.Item, d string, raw []byte, recorded string) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT OR REPLACE INTO memory_item_revisions(item_id,version,state,memory_commit_id,digest,item_json,recorded_at)
		VALUES(?,?,?,?,?,?,?)
	`, it.ID, it.Version, string(it.State), commitID, d, raw, recorded); err != nil {
		return fmt.Errorf("record memory revision: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM memory_dependencies WHERE item_id=?`, it.ID); err != nil {
		return err
	}
	for _, dep := range it.Dependencies {
		if _, err := tx.ExecContext(ctx, `
			INSERT OR REPLACE INTO memory_dependencies(item_id,kind,dependency_id,dependency_version) VALUES(?,?,?,?)
		`, it.ID, dep.Kind, dep.ID, dep.Version); err != nil {
			return fmt.Errorf("record memory dependency: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM memory_evidence WHERE item_id=?`, it.ID); err != nil {
		return err
	}
	for _, ev := range it.Evidence {
		if _, err := tx.ExecContext(ctx, `
			INSERT OR REPLACE INTO memory_evidence(item_id,evidence_id,cluster_id,digest,kind,observed_at) VALUES(?,?,?,?,?,?)
		`, it.ID, ev.ID, ev.ClusterID, ev.Digest, ev.Kind, ev.Observed.UTC().Format(timeLayout)); err != nil {
			return fmt.Errorf("record memory evidence: %w", err)
		}
	}
	return nil
}

// GetMemoryCommit returns one commit and verifies its digest, so a commit
// tampered with in storage is reported rather than returned as canonical.
func (s *Store) GetMemoryCommit(ctx context.Context, id string) (learning.Commit, error) {
	var body []byte
	err := s.db.QueryRowContext(ctx, `SELECT commit_json FROM memory_commits WHERE memory_commit_id=?`, id).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return learning.Commit{}, learning.ErrNotFound
	}
	if err != nil {
		return learning.Commit{}, err
	}
	var c learning.Commit
	if err := json.Unmarshal(body, &c); err != nil {
		return learning.Commit{}, fmt.Errorf("decode memory commit: %w", err)
	}
	if err := c.Verify(); err != nil {
		return learning.Commit{}, err
	}
	return c, nil
}

// GetMemoryItem returns one stored item and verifies its digest.
func (s *Store) GetMemoryItem(ctx context.Context, id string) (learning.Item, error) {
	var body []byte
	var stored string
	err := s.db.QueryRowContext(ctx, `SELECT item_json,digest FROM memory_items WHERE item_id=?`, id).Scan(&body, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return learning.Item{}, learning.ErrNotFound
	}
	if err != nil {
		return learning.Item{}, err
	}
	var it learning.Item
	if err := json.Unmarshal(body, &it); err != nil {
		return learning.Item{}, fmt.Errorf("decode memory item: %w", err)
	}
	got, err := learning.ItemDigest(it)
	if err != nil {
		return learning.Item{}, err
	}
	if got != stored {
		return learning.Item{}, learning.ErrTampered
	}
	return it, nil
}

// ListMemoryItems returns the items visible to one project, including general
// memory. Every returned item has its digest verified.
func (s *Store) ListMemoryItems(ctx context.Context, projectID string, includeGeneral bool) ([]learning.Item, error) {
	query := `SELECT item_json,digest FROM memory_items WHERE (scope='PROJECT' AND project_id=?)`
	if includeGeneral {
		query += ` OR scope='GENERAL'`
	}
	query += ` ORDER BY item_id`
	rows, err := s.db.QueryContext(ctx, query, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []learning.Item
	for rows.Next() {
		var body []byte
		var stored string
		if err := rows.Scan(&body, &stored); err != nil {
			return nil, err
		}
		var it learning.Item
		if err := json.Unmarshal(body, &it); err != nil {
			return nil, fmt.Errorf("decode memory item: %w", err)
		}
		got, err := learning.ItemDigest(it)
		if err != nil {
			return nil, err
		}
		if got != stored {
			return nil, fmt.Errorf("%w: memory item %s", learning.ErrTampered, it.ID)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ItemRevisions returns the full version history of one item, oldest first.
func (s *Store) ItemRevisions(ctx context.Context, id string) ([]learning.Item, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT item_json FROM memory_item_revisions WHERE item_id=? ORDER BY version`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []learning.Item
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var it learning.Item
		if err := json.Unmarshal(body, &it); err != nil {
			return nil, fmt.Errorf("decode memory revision: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ItemsDependingOn returns the items whose stored dependency version differs
// from the supplied one. The lookup is indexed on (kind, dependency_id), so a
// tool upgrade touches only the knowledge that actually rested on that tool.
func (s *Store) ItemsDependingOn(ctx context.Context, dep learning.Dependency) ([]learning.Item, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT i.item_json FROM memory_items i
		JOIN memory_dependencies d ON d.item_id = i.item_id
		WHERE d.kind=? AND d.dependency_id=? AND d.dependency_version<>?
		ORDER BY i.item_id
	`, dep.Kind, dep.ID, dep.Version)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []learning.Item
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var it learning.Item
		if err := json.Unmarshal(body, &it); err != nil {
			return nil, fmt.Errorf("decode memory item: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// RoutingObservations returns every observation for one task class, including
// failed, blocked and unselected routes. Aggregation over a filtered view would
// reintroduce the survivorship bias the model exists to prevent.
func (s *Store) RoutingObservations(ctx context.Context, taskClass string) ([]learning.RoutingObservation, error) {
	query := `SELECT observation_json FROM routing_observations`
	args := []any{}
	if taskClass != "" {
		query += ` WHERE task_class=?`
		args = append(args, taskClass)
	}
	query += ` ORDER BY observation_id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []learning.RoutingObservation
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var o learning.RoutingObservation
		if err := json.Unmarshal(body, &o); err != nil {
			return nil, fmt.Errorf("decode routing observation: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// FailureFingerprints returns the stored fingerprints for a project, general
// fingerprints included.
func (s *Store) FailureFingerprints(ctx context.Context, projectID string) ([]learning.Fingerprint, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT fingerprint_json FROM failure_fingerprints
		WHERE (scope='PROJECT' AND project_id=?) OR scope='GENERAL'
		ORDER BY fingerprint_id
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []learning.Fingerprint
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var f learning.Fingerprint
		if err := json.Unmarshal(body, &f); err != nil {
			return nil, fmt.Errorf("decode failure fingerprint: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// PlaybookCandidates returns candidate procedures. They are always inactive:
// the column is constrained so a stored candidate cannot be active.
func (s *Store) PlaybookCandidates(ctx context.Context, projectID string) ([]learning.PlaybookCandidate, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT candidate_json FROM playbook_candidates
		WHERE (scope='PROJECT' AND project_id=?) OR scope='GENERAL'
		ORDER BY playbook_id
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []learning.PlaybookCandidate
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var p learning.PlaybookCandidate
		if err := json.Unmarshal(body, &p); err != nil {
			return nil, fmt.Errorf("decode playbook candidate: %w", err)
		}
		p.Active = false
		out = append(out, p)
	}
	return out, rows.Err()
}

// AppendReplayRecord indexes one reproducible run.
func (s *Store) AppendReplayRecord(ctx context.Context, commitID string, r learning.ReplayRecord) error {
	if err := learning.ValidateReplay(r); err != nil {
		return err
	}
	body, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO replay_index(replay_id,memory_commit_id,run_id,verification_id,replay_class,tree_digest,environment_digest,evidence_bundle_digest,record_json,recorded_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)
	`, r.ID, commitID, r.Binding.RunID, r.Binding.VerificationID, string(r.Class), r.Binding.TreeDigest,
		r.Binding.EnvironmentDigest, r.Binding.EvidenceDigest, body,
		r.RecordedAt.UTC().Format(timeLayout)); err != nil {
		return fmt.Errorf("append replay record: %w", err)
	}
	return nil
}

// ReplayRecords returns the replay index for one run.
func (s *Store) ReplayRecords(ctx context.Context, runID string) ([]learning.ReplayRecord, error) {
	query := `SELECT record_json FROM replay_index`
	args := []any{}
	if runID != "" {
		query += ` WHERE run_id=?`
		args = append(args, runID)
	}
	query += ` ORDER BY replay_id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []learning.ReplayRecord
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var r learning.ReplayRecord
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, fmt.Errorf("decode replay record: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AppendBenchmarkRecord stores one reproducible evaluation result.
func (s *Store) AppendBenchmarkRecord(ctx context.Context, b learning.BenchmarkRecord) error {
	if err := learning.ValidateBenchmark(b); err != nil {
		return err
	}
	body, err := json.Marshal(b)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO benchmark_records(record_id,benchmark,benchmark_version,task_id,tree_digest,mode,resolved,record_json,recorded_at)
		VALUES(?,?,?,?,?,?,?,?,?)
	`, b.ID, b.Benchmark, b.Version, b.TaskID, b.TreeDigest, b.Mode, string(b.Resolved), body,
		b.RecordedAt.UTC().Format(timeLayout)); err != nil {
		return fmt.Errorf("append benchmark record: %w", err)
	}
	return nil
}

// BenchmarkRecords returns stored evaluation results for one benchmark.
func (s *Store) BenchmarkRecords(ctx context.Context, benchmark string) ([]learning.BenchmarkRecord, error) {
	query := `SELECT record_json FROM benchmark_records`
	args := []any{}
	if benchmark != "" {
		query += ` WHERE benchmark=?`
		args = append(args, benchmark)
	}
	query += ` ORDER BY record_id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []learning.BenchmarkRecord
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var b learning.BenchmarkRecord
		if err := json.Unmarshal(body, &b); err != nil {
			return nil, fmt.Errorf("decode benchmark record: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
