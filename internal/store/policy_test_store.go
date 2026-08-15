package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policy"
	"github.com/Zen1th53/marshal/internal/policytest"
)

type canonicalTestRun struct {
	ID             string                      `json:"id"`
	PolicyID       policy.PolicyID             `json:"policy_id"`
	Binding        policy.PolicyBinding        `json:"binding"`
	TestFileDigest policy.PolicyDigest         `json:"test_file_digest"`
	Cases          []policytest.TestCaseResult `json:"cases"`
}

func canonicalizeTestRun(run policytest.TestRun) (policytest.TestRun, []byte, policy.PolicyDigest, error) {
	if err := run.Validate(); err != nil {
		return policytest.TestRun{}, nil, "", fmt.Errorf("%w: invalid policy test run", model.ErrInvalid)
	}
	clone := policytest.CloneTestRun(run)
	sort.Slice(clone.Cases, func(i, j int) bool { return clone.Cases[i].ID < clone.Cases[j].ID })
	payload, err := json.Marshal(canonicalTestRun{ID: clone.ID, PolicyID: clone.PolicyID, Binding: clone.Binding, TestFileDigest: clone.TestFileDigest, Cases: clone.Cases})
	if err != nil {
		return policytest.TestRun{}, nil, "", fmt.Errorf("%w: serialize policy test run", model.ErrInvalid)
	}
	sum := sha256.Sum256(payload)
	digest := policy.PolicyDigest("sha256:" + hex.EncodeToString(sum[:]))
	return clone, payload, digest, nil
}

// PutPolicyTestRun stores bounded test results as a non-authoritative
// projection. The policy binding is referenced exactly; no policy payload or
// lifecycle state is copied or changed.
func (s *Store) PutPolicyTestRun(ctx context.Context, run policytest.TestRun) error {
	if run.State != "" && run.State != policytest.StateLoaded {
		return fmt.Errorf("%w: policy test runs must start loaded", model.ErrInvalid)
	}
	canonical, _, contentDigest, err := canonicalizeTestRun(run)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("%w: policy test persistence unavailable", model.ErrUnavailable)
	}
	defer tx.Rollback()

	var storedPolicyID, storedPolicyDigest, storedFileDigest, storedCreated, storedContent, storedState string
	var storedVersion, storedGeneration int64
	err = tx.QueryRowContext(ctx, `
		SELECT policy_id, policy_version, policy_digest, generation, test_file_digest, created_at, content_digest, state
		FROM policy_test_runs WHERE run_id = ?
	`, canonical.ID).Scan(&storedPolicyID, &storedVersion, &storedPolicyDigest, &storedGeneration, &storedFileDigest, &storedCreated, &storedContent, &storedState)
	if err == nil {
		if err := policytest.ValidateState(policytest.RunState(storedState)); err != nil {
			return fmt.Errorf("%w: invalid policy test run state", model.ErrInvalid)
		}
		if storedContent != string(contentDigest) || storedPolicyID != string(canonical.PolicyID) || storedVersion != int64(canonical.Binding.Version) || storedPolicyDigest != string(canonical.Binding.Digest) || storedGeneration != int64(canonical.Binding.Generation) || storedFileDigest != string(canonical.TestFileDigest) {
			return fmt.Errorf("%w: policy test run is immutable", model.ErrConflict)
		}
		storedCases, readErr := readPolicyTestCases(ctx, tx, canonical.ID)
		if readErr != nil || len(storedCases) != len(canonical.Cases) {
			return fmt.Errorf("%w: corrupt policy test run", model.ErrInvalid)
		}
		for i := range storedCases {
			if storedCases[i] != canonical.Cases[i] {
				return fmt.Errorf("%w: policy test run is immutable", model.ErrConflict)
			}
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: policy test persistence unavailable", model.ErrUnavailable)
	}

	created := utcNow()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO policy_test_runs(run_id, policy_id, policy_version, policy_digest, generation, test_file_digest, created_at, content_digest, state)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, canonical.ID, string(canonical.PolicyID), int64(canonical.Binding.Version), string(canonical.Binding.Digest), int64(canonical.Binding.Generation), string(canonical.TestFileDigest), created, string(contentDigest), string(policytest.StateLoaded)); err != nil {
		return fmt.Errorf("%w: policy test persistence unavailable", model.ErrUnavailable)
	}
	for _, result := range canonical.Cases {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO policy_test_cases(run_id, case_id, status, diff, reason)
			VALUES(?, ?, ?, ?, ?)
		`, canonical.ID, string(result.ID), string(result.Result.Status), result.Result.Diff, string(result.Result.Reason)); err != nil {
			return fmt.Errorf("%w: policy test persistence unavailable", model.ErrUnavailable)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%w: policy test persistence unavailable", model.ErrUnavailable)
	}
	return nil
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func readPolicyTestCases(ctx context.Context, q queryer, runID string) ([]policytest.TestCaseResult, error) {
	rows, err := q.QueryContext(ctx, `SELECT case_id, status, diff, reason FROM policy_test_cases WHERE run_id = ? ORDER BY case_id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []policytest.TestCaseResult
	for rows.Next() {
		var id, status, diff, reason string
		if err := rows.Scan(&id, &status, &diff, &reason); err != nil {
			return nil, err
		}
		results = append(results, policytest.TestCaseResult{ID: policytest.CaseID(id), Result: policytest.Result{Name: id, Status: policytest.ResultStatus(status), Diff: diff, Reason: policy.ErrorCode(reason)}})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// GetPolicyTestRun reloads and validates one durable, non-authoritative run.
func (s *Store) GetPolicyTestRun(ctx context.Context, id string) (policytest.TestRun, error) {
	if !validTestRunID(id) {
		return policytest.TestRun{}, fmt.Errorf("%w: invalid policy test run id", model.ErrInvalid)
	}
	var policyID, digest, fileDigest, created, contentDigest, stateText string
	var version, generation int64
	err := s.db.QueryRowContext(ctx, `
		SELECT policy_id, policy_version, policy_digest, generation, test_file_digest, created_at, content_digest, state
		FROM policy_test_runs WHERE run_id = ?
	`, id).Scan(&policyID, &version, &digest, &generation, &fileDigest, &created, &contentDigest, &stateText)
	if errors.Is(err, sql.ErrNoRows) {
		return policytest.TestRun{}, fmt.Errorf("%w: policy test run not found", model.ErrNotFound)
	}
	if err != nil {
		return policytest.TestRun{}, fmt.Errorf("%w: policy test persistence unavailable", model.ErrUnavailable)
	}
	parsedTime, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return policytest.TestRun{}, fmt.Errorf("%w: invalid policy test run timestamp", model.ErrInvalid)
	}
	cases, err := readPolicyTestCases(ctx, s.db, id)
	if err != nil {
		return policytest.TestRun{}, fmt.Errorf("%w: invalid policy test cases", model.ErrInvalid)
	}
	state := policytest.RunState(stateText)
	if err := policytest.ValidateState(state); err != nil {
		return policytest.TestRun{}, fmt.Errorf("%w: invalid policy test run state", model.ErrInvalid)
	}
	run := policytest.TestRun{ID: id, PolicyID: policy.PolicyID(policyID), Binding: policy.PolicyBinding{Version: policy.PolicyVersion(version), Digest: policy.PolicyDigest(digest), Generation: uint64(generation)}, TestFileDigest: policy.PolicyDigest(fileDigest), Cases: cases, State: state, CreatedAt: parsedTime.UTC()}
	_, _, computed, err := canonicalizeTestRun(run)
	if err != nil || computed != policy.PolicyDigest(contentDigest) {
		return policytest.TestRun{}, fmt.Errorf("%w: invalid policy test run content", model.ErrInvalid)
	}
	return policytest.CloneTestRun(run), nil
}

// transitionPolicyTestRunState is the store-internal A03 lifecycle primitive.
// Callers must use the authorized A04 boundary below.
func (s *Store) transitionPolicyTestRunState(ctx context.Context, runID string, expected, target policytest.RunState) (policytest.TestRun, error) {
	if !validTestRunID(runID) {
		return policytest.TestRun{}, fmt.Errorf("%w: invalid policy test run id", model.ErrInvalid)
	}
	if err := policytest.ValidateTransition(expected, target); err != nil {
		return policytest.TestRun{}, fmt.Errorf("%w: invalid policy test transition", model.ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return policytest.TestRun{}, fmt.Errorf("%w: policy test transition unavailable", model.ErrUnavailable)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE policy_test_runs SET state = ? WHERE run_id = ? AND state = ?
	`, string(target), runID, string(expected))
	if err != nil {
		return policytest.TestRun{}, fmt.Errorf("%w: policy test transition unavailable", model.ErrUnavailable)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return policytest.TestRun{}, fmt.Errorf("%w: policy test transition unavailable", model.ErrUnavailable)
	}
	if affected != 1 {
		var current string
		err := tx.QueryRowContext(ctx, "SELECT state FROM policy_test_runs WHERE run_id = ?", runID).Scan(&current)
		if errors.Is(err, sql.ErrNoRows) {
			return policytest.TestRun{}, fmt.Errorf("%w: policy test run not found", model.ErrNotFound)
		}
		if err != nil {
			return policytest.TestRun{}, fmt.Errorf("%w: policy test transition unavailable", model.ErrUnavailable)
		}
		if policytest.ValidateState(policytest.RunState(current)) != nil {
			return policytest.TestRun{}, fmt.Errorf("%w: invalid policy test run state", model.ErrInvalid)
		}
		return policytest.TestRun{}, fmt.Errorf("%w: stale policy test run state", model.ErrConflict)
	}
	if err := tx.Commit(); err != nil {
		return policytest.TestRun{}, fmt.Errorf("%w: policy test transition unavailable", model.ErrUnavailable)
	}
	return s.GetPolicyTestRun(ctx, runID)
}

// TransitionPolicyTestRunStateAuthorized is the sole exported T49 lifecycle
// mutation boundary. It binds an authorizer decision to the canonical run and
// then delegates to the A03 expected-state CAS primitive.
func (s *Store) TransitionPolicyTestRunStateAuthorized(ctx context.Context, request policytest.AuthorizationRequest, authorizer policytest.Authorizer) (policytest.TestRun, error) {
	if err := request.Validate(); err != nil {
		return policytest.TestRun{}, err
	}
	if authorizer == nil {
		return policytest.TestRun{}, policy.ErrAuthorizationUnavailable
	}
	run, err := s.GetPolicyTestRun(ctx, request.RunID)
	if err != nil {
		return policytest.TestRun{}, err
	}
	if run.PolicyID != request.PolicyID || run.Binding != request.Binding || run.TestFileDigest != request.TestFileDigest || run.State != request.ExpectedState {
		return policytest.TestRun{}, policy.ErrAuthorizationStale
	}
	decision, err := authorizer.AuthorizePolicyTestRun(ctx, request)
	if err != nil {
		return policytest.TestRun{}, policy.ErrAuthorizationUnavailable
	}
	if err := decision.ValidateFor(request); err != nil {
		return policytest.TestRun{}, err
	}
	return s.transitionPolicyTestRunState(ctx, request.RunID, request.ExpectedState, request.TargetState)
}

func validTestRunID(value string) bool {
	if value == "" || len(value) > 128 || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) || r == '/' {
			return false
		}
	}
	return true
}
