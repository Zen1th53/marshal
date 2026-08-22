package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// MemoryRuntimeCandidate is bounded, content-free attribution for one recall
// candidate. It is deliberately separate from canonical memory content.
type MemoryRuntimeCandidate struct {
	MemoryID     string   `json:"memory_id"`
	RankScore    float64  `json:"rank_score"`
	UtilityScore float64  `json:"utility_score"`
	Tokens       int      `json:"tokens"`
	Decision     string   `json:"decision"`
	Reasons      []string `json:"reasons,omitempty"`
}

type MemoryRuntimeTrace struct {
	RunID             string                   `json:"run_id"`
	ProjectID         string                   `json:"project_id"`
	TaskID            string                   `json:"task_id"`
	QueryDigest       string                   `json:"query_digest"`
	HeadCommit        string                   `json:"head_commit"`
	Candidates        []MemoryRuntimeCandidate `json:"candidates"`
	AdmittedMemoryIDs []string                 `json:"admitted_memory_ids"`
	TokensRequested   int                      `json:"tokens_requested"`
	TokensAdmitted    int                      `json:"tokens_admitted"`
	CreatedAt         time.Time                `json:"created_at"`
}

func (s *Store) PutMemoryRuntimeTrace(ctx context.Context, trace MemoryRuntimeTrace) error {
	if trace.RunID == "" || trace.ProjectID == "" || trace.TaskID == "" || trace.QueryDigest == "" || trace.CreatedAt.IsZero() {
		return fmt.Errorf("%w: incomplete memory runtime trace", model.ErrInvalid)
	}
	if trace.TokensRequested < 0 || trace.TokensAdmitted < 0 {
		return fmt.Errorf("%w: negative memory runtime trace tokens", model.ErrInvalid)
	}
	candidates, err := json.Marshal(trace.Candidates)
	if err != nil {
		return fmt.Errorf("encode memory runtime candidates: %w", err)
	}
	admitted, err := json.Marshal(trace.AdmittedMemoryIDs)
	if err != nil {
		return fmt.Errorf("encode admitted memory IDs: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO memory_runtime_traces(
			run_id, project_id, task_id, query_digest, head_commit, candidates_json,
			admitted_memory_ids_json, tokens_requested, tokens_admitted, created_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(run_id) DO UPDATE SET
			candidates_json=excluded.candidates_json,
			admitted_memory_ids_json=excluded.admitted_memory_ids_json,
			tokens_requested=excluded.tokens_requested,
			tokens_admitted=excluded.tokens_admitted,
			head_commit=excluded.head_commit
	`, trace.RunID, trace.ProjectID, trace.TaskID, trace.QueryDigest, trace.HeadCommit,
		string(candidates), string(admitted), trace.TokensRequested, trace.TokensAdmitted,
		trace.CreatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("store memory runtime trace: %w", err)
	}
	return nil
}

func (s *Store) LatestMemoryRuntimeTrace(ctx context.Context, projectID, taskID string) (MemoryRuntimeTrace, error) {
	if projectID == "" || taskID == "" {
		return MemoryRuntimeTrace{}, fmt.Errorf("%w: project and task are required", model.ErrInvalid)
	}
	var trace MemoryRuntimeTrace
	var candidates, admitted, created string
	err := s.db.QueryRowContext(ctx, `
		SELECT run_id, project_id, task_id, query_digest, head_commit, candidates_json,
		       admitted_memory_ids_json, tokens_requested, tokens_admitted, created_at
		FROM memory_runtime_traces
		WHERE project_id = ? AND task_id = ?
		ORDER BY created_at DESC, run_id DESC LIMIT 1
	`, projectID, taskID).Scan(&trace.RunID, &trace.ProjectID, &trace.TaskID, &trace.QueryDigest,
		&trace.HeadCommit, &candidates, &admitted, &trace.TokensRequested, &trace.TokensAdmitted, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return MemoryRuntimeTrace{}, fmt.Errorf("%w: memory runtime trace", model.ErrNotFound)
	}
	if err != nil {
		return MemoryRuntimeTrace{}, fmt.Errorf("read memory runtime trace: %w", err)
	}
	if err := json.Unmarshal([]byte(candidates), &trace.Candidates); err != nil {
		return MemoryRuntimeTrace{}, fmt.Errorf("decode memory runtime candidates: %w", err)
	}
	if err := json.Unmarshal([]byte(admitted), &trace.AdmittedMemoryIDs); err != nil {
		return MemoryRuntimeTrace{}, fmt.Errorf("decode admitted memory IDs: %w", err)
	}
	trace.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return MemoryRuntimeTrace{}, fmt.Errorf("decode memory runtime trace time: %w", err)
	}
	return trace, nil
}

// RecordMemoryRuntimeOutcome atomically records each admitted memory at most
// once per run and updates durable utility counters. Replayed completion work
// therefore cannot amplify a memory's ranking influence.
func (s *Store) RecordMemoryRuntimeOutcome(ctx context.Context, projectID, taskID, runID string, memoryIDs []string, success bool) error {
	if projectID == "" || taskID == "" || runID == "" {
		return fmt.Errorf("%w: incomplete memory runtime outcome", model.ErrInvalid)
	}
	unique := make(map[string]struct{}, len(memoryIDs))
	for _, id := range memoryIDs {
		if id != "" {
			unique[id] = struct{}{}
		}
	}
	ids := make([]string, 0, len(unique))
	for id := range unique {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin memory runtime outcome: %w", err)
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	successValue := 0
	if success {
		successValue = 1
	}
	for _, id := range ids {
		result, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO memory_runtime_outcomes(run_id, memory_id, project_id, task_id, success, created_at)
			VALUES(?, ?, ?, ?, ?, ?)
		`, runID, id, projectID, taskID, successValue, now)
		if err != nil {
			return fmt.Errorf("record memory runtime outcome: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count memory runtime outcome: %w", err)
		}
		if changed == 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO memory_utility_scores(project_id, memory_id, success_count, failure_count, last_used_at)
			VALUES(?, ?, ?, ?, ?)
			ON CONFLICT(project_id, memory_id) DO UPDATE SET
				success_count=success_count + excluded.success_count,
				failure_count=failure_count + excluded.failure_count,
				last_used_at=excluded.last_used_at
		`, projectID, id, successValue, 1-successValue, now); err != nil {
			return fmt.Errorf("update memory utility score: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit memory runtime outcome: %w", err)
	}
	return nil
}

// MemoryUtilityScore returns a Laplace-smoothed score with a neutral prior.
func (s *Store) MemoryUtilityScore(ctx context.Context, projectID, memoryID string) (float64, error) {
	var successes, failures int
	err := s.db.QueryRowContext(ctx, `
		SELECT success_count, failure_count FROM memory_utility_scores
		WHERE project_id = ? AND memory_id = ?
	`, projectID, memoryID).Scan(&successes, &failures)
	if errors.Is(err, sql.ErrNoRows) {
		return 0.5, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read memory utility score: %w", err)
	}
	return float64(1+successes) / float64(2+successes+failures), nil
}

// HasMemoryV2ForRun is the idempotency guard for terminal capture. Run IDs
// are canonical runtime identities, unlike provider output or prose hashes.
func (s *Store) HasMemoryV2ForRun(ctx context.Context, projectID, runID string, kind model.MemoryKind) (bool, error) {
	if projectID == "" || runID == "" || !kind.IsValid() {
		return false, fmt.Errorf("%w: incomplete memory run lookup", model.ErrInvalid)
	}
	var exists int
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM memory_records_v2
			WHERE project_id = ? AND run_id = ? AND kind = ?
		)
	`, projectID, runID, string(kind)).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("lookup memory run capture: %w", err)
	}
	return exists == 1, nil
}
