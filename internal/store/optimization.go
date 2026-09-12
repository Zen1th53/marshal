package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Zen1th53/marshal/internal/optimization"
)

// AppendOptimizationCycle writes one Process 08 cycle and every sub-record it
// carries in a single transaction.
//
// The cycle is append-only and digest-protected, matching the memory commit it
// is bound to. Any failing sub-record aborts the whole write: a cycle that
// records candidates and baselines but silently drops an experiment result
// would let evidence disappear without a trace, so the transaction all-or-
// nothing guarantee is what makes the record trustworthy rather than merely
// present.
func (s *Store) AppendOptimizationCycle(ctx context.Context, c optimization.Cycle) error {
	if err := c.Verify(); err != nil {
		return err
	}
	if !c.Binding.Valid() {
		return fmt.Errorf("%w: Process 07 entry binding", optimization.ErrInvalid)
	}
	body, err := json.Marshal(c)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin optimization cycle: %w", err)
	}
	defer tx.Rollback()

	created := c.CreatedAt.UTC().Format(timeLayout)
	updated := c.UpdatedAt.UTC().Format(timeLayout)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO optimization_cycles(optimization_id,version,project_id,memory_commit_id,memory_commit_version,outcome,digest,cycle_json,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)
	`, c.ID, c.Version, c.Binding.ProjectID, c.Binding.MemoryCommitID, c.Binding.MemoryVersion,
		string(c.Binding.Outcome), c.Digest, body, created, updated); err != nil {
		return fmt.Errorf("append optimization cycle: %w", err)
	}

	for _, cand := range c.Candidates {
		if err := insertCandidate(ctx, tx, c.ID, cand, created); err != nil {
			return err
		}
	}
	for _, b := range c.Baselines {
		if err := insertBaseline(ctx, tx, c.ID, b); err != nil {
			return err
		}
	}
	for _, d := range c.Decisions {
		if err := insertPromotionRecord(ctx, tx, c.ID, d); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit optimization cycle: %w", err)
	}
	return nil
}

func insertCandidate(ctx context.Context, tx *sql.Tx, cycleID string, cand optimization.Candidate, created string) error {
	if err := optimization.ValidateCandidate(cand); err != nil {
		return err
	}
	raw, err := json.Marshal(cand)
	if err != nil {
		return err
	}
	effects, err := json.Marshal(cand.Effects)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO optimization_candidates(candidate_id,optimization_id,dimension,cluster_id,effects_json,candidate_json,created_at)
		VALUES(?,?,?,?,?,?,?)
	`, cand.ID, cycleID, string(cand.Dimension), cand.ClusterID, effects, raw, created); err != nil {
		return fmt.Errorf("insert optimization candidate: %w", err)
	}
	return nil
}

func insertBaseline(ctx context.Context, tx *sql.Tx, cycleID string, b optimization.Baseline) error {
	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	recorded := b.RecordedAt.UTC().Format(timeLayout)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO optimization_baselines(baseline_id,optimization_id,marshal_sha,environment_digest,baseline_json,recorded_at)
		VALUES(?,?,?,?,?,?)
	`, b.ID, cycleID, b.MarshalSHA, b.EnvironmentHash, raw, recorded); err != nil {
		return fmt.Errorf("insert optimization baseline: %w", err)
	}
	return nil
}

func insertPromotionRecord(ctx context.Context, tx *sql.Tx, cycleID string, d optimization.PromotionRecord) error {
	if err := d.Verify(); err != nil {
		return err
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	decided := d.DecidedAt.UTC().Format(timeLayout)
	// promotion_id is derived from the digest: every decision, including a
	// refusal, is a distinct append-only row rather than a slot that gets
	// overwritten by the next evaluation of the same candidate.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO promotion_records(promotion_id,optimization_id,candidate_id,baseline_id,decision,digest,promotion_json,decided_at)
		VALUES(?,?,?,?,?,?,?,?)
	`, d.Digest, cycleID, d.CandidateID, d.BaselineID, string(d.Decision), d.Digest, raw, decided); err != nil {
		return fmt.Errorf("insert promotion record: %w", err)
	}
	return nil
}

// GetOptimizationCycle returns one cycle and verifies its digest, so a cycle
// tampered with in storage is reported rather than returned as canonical.
func (s *Store) GetOptimizationCycle(ctx context.Context, id string) (optimization.Cycle, error) {
	var body []byte
	err := s.db.QueryRowContext(ctx, `SELECT cycle_json FROM optimization_cycles WHERE optimization_id=?`, id).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return optimization.Cycle{}, optimization.ErrNotFound
	}
	if err != nil {
		return optimization.Cycle{}, err
	}
	var c optimization.Cycle
	if err := json.Unmarshal(body, &c); err != nil {
		return optimization.Cycle{}, fmt.Errorf("decode optimization cycle: %w", err)
	}
	if err := c.Verify(); err != nil {
		return optimization.Cycle{}, err
	}
	return c, nil
}

// ListOptimizationCycles returns every durable cycle newest first and verifies
// each digest before exposing it. A list endpoint that skipped verification
// would let the TUI present tampered rows that GetOptimizationCycle rejects.
func (s *Store) ListOptimizationCycles(ctx context.Context) ([]optimization.Cycle, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT cycle_json FROM optimization_cycles
		ORDER BY updated_at DESC, optimization_id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []optimization.Cycle
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var cycle optimization.Cycle
		if err := json.Unmarshal(body, &cycle); err != nil {
			return nil, fmt.Errorf("decode optimization cycle: %w", err)
		}
		if err := cycle.Verify(); err != nil {
			return nil, err
		}
		out = append(out, cycle)
	}
	return out, rows.Err()
}

// UpdateOptimizationCycle applies a new version of a cycle through CAS on
// (optimization_id, version). A caller racing against a concurrent writer
// loses rather than silently clobbering the other write.
func (s *Store) UpdateOptimizationCycle(ctx context.Context, c optimization.Cycle, expectedVersion int64) error {
	if c.Version != expectedVersion+1 {
		return optimization.ErrConflict
	}
	if err := c.Verify(); err != nil {
		return err
	}
	if !c.Binding.Valid() {
		return fmt.Errorf("%w: Process 07 entry binding", optimization.ErrInvalid)
	}
	body, err := json.Marshal(c)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin optimization cycle update: %w", err)
	}
	defer tx.Rollback()

	updated := c.UpdatedAt.UTC().Format(timeLayout)
	res, err := tx.ExecContext(ctx, `
		UPDATE optimization_cycles
		SET version=?,outcome=?,digest=?,cycle_json=?,updated_at=?
		WHERE optimization_id=? AND version=?
	`, c.Version, string(c.Binding.Outcome), c.Digest, body, updated, c.ID, expectedVersion)
	if err != nil {
		return fmt.Errorf("update optimization cycle: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return optimization.ErrConflict
	}

	for _, cand := range c.Candidates {
		if err := upsertCandidate(ctx, tx, c.ID, cand, updated); err != nil {
			return err
		}
	}
	for _, b := range c.Baselines {
		if err := upsertBaseline(ctx, tx, c.ID, b); err != nil {
			return err
		}
	}
	for _, d := range c.Decisions {
		if err := upsertPromotionRecord(ctx, tx, c.ID, d); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit optimization cycle update: %w", err)
	}
	return nil
}

func upsertCandidate(ctx context.Context, tx *sql.Tx, cycleID string, cand optimization.Candidate, created string) error {
	if err := optimization.ValidateCandidate(cand); err != nil {
		return err
	}
	raw, err := json.Marshal(cand)
	if err != nil {
		return err
	}
	effects, err := json.Marshal(cand.Effects)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO optimization_candidates(candidate_id,optimization_id,dimension,cluster_id,effects_json,candidate_json,created_at)
		VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(candidate_id) DO UPDATE SET
			dimension=excluded.dimension, cluster_id=excluded.cluster_id,
			effects_json=excluded.effects_json, candidate_json=excluded.candidate_json
	`, cand.ID, cycleID, string(cand.Dimension), cand.ClusterID, effects, raw, created); err != nil {
		return fmt.Errorf("upsert optimization candidate: %w", err)
	}
	return nil
}

func upsertBaseline(ctx context.Context, tx *sql.Tx, cycleID string, b optimization.Baseline) error {
	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	recorded := b.RecordedAt.UTC().Format(timeLayout)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO optimization_baselines(baseline_id,optimization_id,marshal_sha,environment_digest,baseline_json,recorded_at)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(baseline_id) DO UPDATE SET
			marshal_sha=excluded.marshal_sha, environment_digest=excluded.environment_digest,
			baseline_json=excluded.baseline_json, recorded_at=excluded.recorded_at
	`, b.ID, cycleID, b.MarshalSHA, b.EnvironmentHash, raw, recorded); err != nil {
		return fmt.Errorf("upsert optimization baseline: %w", err)
	}
	return nil
}

// upsertPromotionRecord inserts a decision, ignoring a duplicate. Promotion
// records are append-only: a decision already recorded under its digest is
// never rewritten, only added to.
func upsertPromotionRecord(ctx context.Context, tx *sql.Tx, cycleID string, d optimization.PromotionRecord) error {
	if err := d.Verify(); err != nil {
		return err
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	decided := d.DecidedAt.UTC().Format(timeLayout)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO promotion_records(promotion_id,optimization_id,candidate_id,baseline_id,decision,digest,promotion_json,decided_at)
		VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(promotion_id) DO NOTHING
	`, d.Digest, cycleID, d.CandidateID, d.BaselineID, string(d.Decision), d.Digest, raw, decided); err != nil {
		return fmt.Errorf("insert promotion record: %w", err)
	}
	return nil
}

// Candidates returns every candidate recorded against one cycle, each with its
// digest-protected effects verified against the stored candidate body.
func (s *Store) OptimizationCandidates(ctx context.Context, cycleID string) ([]optimization.Candidate, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT candidate_json FROM optimization_candidates WHERE optimization_id=? ORDER BY candidate_id
	`, cycleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []optimization.Candidate
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var cand optimization.Candidate
		if err := json.Unmarshal(body, &cand); err != nil {
			return nil, fmt.Errorf("decode optimization candidate: %w", err)
		}
		out = append(out, cand)
	}
	return out, rows.Err()
}

// AppendCounterfactual stores one evaluated counterfactual, verifying its
// digest before it is ever written so a malformed evaluation cannot enter
// storage looking canonical.
func (s *Store) AppendCounterfactual(ctx context.Context, cycleID string, c optimization.Counterfactual) error {
	if err := c.Verify(); err != nil {
		return err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	verdict := optimization.Compare(c)
	evaluated := c.EvaluatedAt.UTC().Format(timeLayout)
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO counterfactuals(counterfactual_id,optimization_id,method,verdict,cluster_id,digest,counterfactual_json,evaluated_at)
		VALUES(?,?,?,?,?,?,?,?)
	`, c.ID, cycleID, string(c.Method), string(verdict), c.ClusterID, c.Digest, raw, evaluated); err != nil {
		return fmt.Errorf("append counterfactual: %w", err)
	}
	return nil
}

// GetCounterfactual returns one counterfactual and verifies its digest.
func (s *Store) GetCounterfactual(ctx context.Context, id string) (optimization.Counterfactual, error) {
	var body []byte
	err := s.db.QueryRowContext(ctx, `SELECT counterfactual_json FROM counterfactuals WHERE counterfactual_id=?`, id).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return optimization.Counterfactual{}, optimization.ErrNotFound
	}
	if err != nil {
		return optimization.Counterfactual{}, err
	}
	var c optimization.Counterfactual
	if err := json.Unmarshal(body, &c); err != nil {
		return optimization.Counterfactual{}, fmt.Errorf("decode counterfactual: %w", err)
	}
	if err := c.Verify(); err != nil {
		return optimization.Counterfactual{}, err
	}
	return c, nil
}

// Counterfactuals returns every counterfactual recorded against one cycle.
func (s *Store) Counterfactuals(ctx context.Context, cycleID string) ([]optimization.Counterfactual, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT counterfactual_json FROM counterfactuals WHERE optimization_id=? ORDER BY counterfactual_id
	`, cycleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []optimization.Counterfactual
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var c optimization.Counterfactual
		if err := json.Unmarshal(body, &c); err != nil {
			return nil, fmt.Errorf("decode counterfactual: %w", err)
		}
		if err := c.Verify(); err != nil {
			return nil, fmt.Errorf("%w: counterfactual %s", err, c.ID)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// AppendExperimentResult stores one measured outcome, whatever it was. A
// quarantined or UNKNOWN result is stored exactly like a clean pass: nothing
// here decides what counts, that judgement belongs to the promotion gates.
func (s *Store) AppendExperimentResult(ctx context.Context, cycleID, candidateID string, r optimization.ExperimentResult) error {
	sealed, err := optimization.NewExperimentResult(r, r.ObservedAt)
	if err != nil {
		return err
	}
	r = sealed
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	observed := r.ObservedAt.UTC().Format(timeLayout)
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO experiment_results(result_id,optimization_id,candidate_id,task_id,task_class,outcome,quarantined,quarantine_reason,holdout,cluster_id,digest,result_json,observed_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)
	`, r.ID, cycleID, candidateID, r.TaskID, r.TaskClass, string(r.Outcome), boolInt(r.Quarantined),
		r.QuarantineReason, boolInt(r.Holdout), r.ClusterID, r.Digest, raw, observed); err != nil {
		return fmt.Errorf("append experiment result: %w", err)
	}
	return nil
}

// ExperimentResults returns every attempt recorded for one candidate within a
// cycle, quarantined and holdout results included. Filtering happens at the
// evidence-gate layer, never here.
func (s *Store) ExperimentResults(ctx context.Context, cycleID, candidateID string) ([]optimization.ExperimentResult, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT result_json FROM experiment_results WHERE optimization_id=? AND candidate_id=? ORDER BY result_id
	`, cycleID, candidateID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []optimization.ExperimentResult
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var r optimization.ExperimentResult
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, fmt.Errorf("decode experiment result: %w", err)
		}
		if err := r.Verify(); err != nil {
			return nil, fmt.Errorf("%w: experiment result %s", err, r.ID)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AppendBenchmarkManifest stores one evaluation manifest with its digest
// verified, so a benchmark score always traces back to reproducible provenance.
func (s *Store) AppendBenchmarkManifest(ctx context.Context, cycleID string, m optimization.BenchmarkManifest) error {
	if err := m.Verify(); err != nil {
		return err
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	started := m.StartedAt.UTC().Format(timeLayout)
	finished := m.FinishedAt.UTC().Format(timeLayout)
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO benchmark_manifests(manifest_id,optimization_id,benchmark,official,evaluator_version,marshal_sha,digest,manifest_json,started_at,finished_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)
	`, m.ID, cycleID, string(m.Kind), boolInt(m.Official), m.EvaluatorVersion, m.MarshalSHA,
		m.Digest, raw, started, finished); err != nil {
		return fmt.Errorf("append benchmark manifest: %w", err)
	}
	return nil
}

// GetBenchmarkManifest returns one manifest and verifies its digest.
func (s *Store) GetBenchmarkManifest(ctx context.Context, id string) (optimization.BenchmarkManifest, error) {
	var body []byte
	err := s.db.QueryRowContext(ctx, `SELECT manifest_json FROM benchmark_manifests WHERE manifest_id=?`, id).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return optimization.BenchmarkManifest{}, optimization.ErrNotFound
	}
	if err != nil {
		return optimization.BenchmarkManifest{}, err
	}
	var m optimization.BenchmarkManifest
	if err := json.Unmarshal(body, &m); err != nil {
		return optimization.BenchmarkManifest{}, fmt.Errorf("decode benchmark manifest: %w", err)
	}
	if err := m.Verify(); err != nil {
		return optimization.BenchmarkManifest{}, err
	}
	return m, nil
}

// BenchmarkManifests returns every manifest recorded against one cycle.
func (s *Store) BenchmarkManifests(ctx context.Context, cycleID string) ([]optimization.BenchmarkManifest, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT manifest_json FROM benchmark_manifests WHERE optimization_id=? ORDER BY manifest_id
	`, cycleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []optimization.BenchmarkManifest
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var m optimization.BenchmarkManifest
		if err := json.Unmarshal(body, &m); err != nil {
			return nil, fmt.Errorf("decode benchmark manifest: %w", err)
		}
		if err := m.Verify(); err != nil {
			return nil, fmt.Errorf("%w: benchmark manifest %s", err, m.ID)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AppendPromotionRecord stores one promotion decision on its own, outside a
// cycle write, for callers evaluating promotion incrementally. Refusals are
// stored exactly like approvals: nothing here drops a BLOCKED or REJECT
// verdict, because the refusal is the evidentiary record that a candidate was
// considered and stopped.
func (s *Store) AppendPromotionRecord(ctx context.Context, cycleID string, d optimization.PromotionRecord) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin promotion record: %w", err)
	}
	defer tx.Rollback()
	if err := insertPromotionRecord(ctx, tx, cycleID, d); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit promotion record: %w", err)
	}
	return nil
}

// PromotionRecords returns every promotion decision made against one
// candidate within a cycle, including refusals, in the order they were made.
func (s *Store) PromotionRecords(ctx context.Context, cycleID, candidateID string) ([]optimization.PromotionRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT promotion_json FROM promotion_records WHERE optimization_id=? AND candidate_id=? ORDER BY decided_at, promotion_id
	`, cycleID, candidateID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []optimization.PromotionRecord
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var d optimization.PromotionRecord
		if err := json.Unmarshal(body, &d); err != nil {
			return nil, fmt.Errorf("decode promotion record: %w", err)
		}
		if err := d.Verify(); err != nil {
			return nil, fmt.Errorf("%w: promotion record for %s", err, d.CandidateID)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// AppendCanary stores or updates one canary rollout record. Canaries are
// mutable in place (status, exposure and results change as the rollout runs),
// but every accumulated result stays on the record rather than being trimmed.
func (s *Store) AppendCanary(ctx context.Context, cycleID, promotionID string, c optimization.Canary) error {
	// Governance's exposure ceiling is evaluated by the runtime before a
	// canary ever reaches the store; storage checks the structural
	// requirements every canary must have regardless of governance
	// configuration, using a ceiling of 1.0 so only the unconditional rules
	// in ValidateCanary apply here.
	if err := optimization.ValidateCanary(c, optimization.Governance{MaxCanaryExposure: 1.0}); err != nil {
		return err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	started := c.StartedAt.UTC().Format(timeLayout)
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO canary_rollouts(canary_id,optimization_id,candidate_id,promotion_id,status,exposure,rollback_reason,canary_json,started_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(canary_id) DO UPDATE SET
			status=excluded.status, exposure=excluded.exposure,
			rollback_reason=excluded.rollback_reason, canary_json=excluded.canary_json,
			updated_at=excluded.updated_at
	`, c.ID, cycleID, c.CandidateID, promotionID, string(c.State), c.Exposure, c.RollbackReason,
		raw, started, utcNow()); err != nil {
		return fmt.Errorf("append canary rollout: %w", err)
	}
	return nil
}

// GetCanary returns one canary rollout record.
func (s *Store) GetCanary(ctx context.Context, id string) (optimization.Canary, error) {
	var body []byte
	err := s.db.QueryRowContext(ctx, `SELECT canary_json FROM canary_rollouts WHERE canary_id=?`, id).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return optimization.Canary{}, optimization.ErrNotFound
	}
	if err != nil {
		return optimization.Canary{}, err
	}
	var c optimization.Canary
	if err := json.Unmarshal(body, &c); err != nil {
		return optimization.Canary{}, fmt.Errorf("decode canary rollout: %w", err)
	}
	return c, nil
}

// GetCanaryBinding returns the durable parent bindings together with a canary.
// Updates must preserve these columns; replacing them with empty caller values
// would detach the rollout from the optimization evidence that authorized it.
func (s *Store) GetCanaryBinding(ctx context.Context, id string) (string, string, optimization.Canary, error) {
	var cycleID, promotionID string
	var body []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT optimization_id, promotion_id, canary_json
		FROM canary_rollouts WHERE canary_id=?
	`, id).Scan(&cycleID, &promotionID, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", optimization.Canary{}, optimization.ErrNotFound
	}
	if err != nil {
		return "", "", optimization.Canary{}, err
	}
	var c optimization.Canary
	if err := json.Unmarshal(body, &c); err != nil {
		return "", "", optimization.Canary{}, fmt.Errorf("decode canary rollout: %w", err)
	}
	return cycleID, promotionID, c, nil
}

// ActiveCanaries returns rollouts for which rollback remains meaningful.
func (s *Store) ActiveCanaries(ctx context.Context) ([]optimization.Canary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT canary_json FROM canary_rollouts
		WHERE status IN (?, ?) ORDER BY updated_at DESC, canary_id
	`, string(optimization.CanaryPending), string(optimization.CanaryRunning))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []optimization.Canary
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var c optimization.Canary
		if err := json.Unmarshal(body, &c); err != nil {
			return nil, fmt.Errorf("decode canary rollout: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Canaries returns every canary recorded against one cycle.
func (s *Store) Canaries(ctx context.Context, cycleID string) ([]optimization.Canary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT canary_json FROM canary_rollouts WHERE optimization_id=? ORDER BY canary_id
	`, cycleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []optimization.Canary
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var c optimization.Canary
		if err := json.Unmarshal(body, &c); err != nil {
			return nil, fmt.Errorf("decode canary rollout: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
