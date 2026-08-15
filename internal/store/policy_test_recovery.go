package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policy"
	"github.com/Zen1th53/marshal/internal/policytest"
)

const policyTestExecutionClaimTTL = 5 * time.Minute

func newPolicyTestExecutionOwner() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("%w: generate policy test execution owner", model.ErrUnavailable)
	}
	return "execution-" + hex.EncodeToString(value[:]), nil
}

func (s *Store) claimPolicyTestExecution(ctx context.Context, runID string) (string, error) {
	owner, err := newPolicyTestExecutionOwner()
	if err != nil {
		return "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("%w: policy test execution claim unavailable", model.ErrUnavailable)
	}
	defer tx.Rollback()
	cutoff := time.Now().UTC().Add(-policyTestExecutionClaimTTL).Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, `
		UPDATE policy_test_runs
		SET execution_owner = ?, execution_claimed_at = ?
		WHERE run_id = ? AND state = 'executed'
		  AND (execution_owner = '' OR execution_claimed_at = '' OR execution_claimed_at < ?)
	`, owner, utcNow(), runID, cutoff)
	if err != nil {
		return "", fmt.Errorf("%w: policy test execution claim unavailable", model.ErrUnavailable)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return "", fmt.Errorf("%w: policy test execution claim unavailable", model.ErrUnavailable)
	}
	if affected != 1 {
		return "", fmt.Errorf("%w: policy test execution already claimed", model.ErrConflict)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("%w: policy test execution claim unavailable", model.ErrUnavailable)
	}
	return owner, nil
}

func (s *Store) releasePolicyTestExecution(ctx context.Context, runID, owner string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE policy_test_runs
		SET execution_owner = '', execution_claimed_at = ''
		WHERE run_id = ? AND state = 'executed' AND execution_owner = ?
	`, runID, owner)
	return err
}

func persistPolicyTestOutcomesTx(ctx context.Context, tx *sql.Tx, runID string, result policytest.RunResult) error {
	for _, caseResult := range result.Cases {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO policy_test_outcomes(run_id, case_id, status, diff, reason)
			VALUES(?, ?, ?, ?, ?)
		`, runID, string(caseResult.ID), string(caseResult.Result.Status), caseResult.Result.Diff, string(caseResult.Result.Reason)); err != nil {
			return fmt.Errorf("%w: persist policy test result", model.ErrUnavailable)
		}
	}
	return nil
}

func (s *Store) recoveredPolicyTestResult(ctx context.Context, run policytest.TestRun) (policytest.RunResult, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT case_id, status, diff, reason
		FROM policy_test_outcomes WHERE run_id = ? ORDER BY case_id
	`, run.ID)
	if err != nil {
		return policytest.RunResult{}, fmt.Errorf("%w: read policy test outcomes", model.ErrUnavailable)
	}
	defer rows.Close()
	var results []policytest.TestCaseResult
	for rows.Next() {
		var id, status, diff, reason string
		if err := rows.Scan(&id, &status, &diff, &reason); err != nil {
			return policytest.RunResult{}, fmt.Errorf("%w: read policy test outcomes", model.ErrUnavailable)
		}
		results = append(results, policytest.TestCaseResult{ID: policytest.CaseID(id), Result: policytest.Result{Name: id, Status: policytest.ResultStatus(status), Diff: diff, Reason: policy.ErrorCode(reason)}})
	}
	if err := rows.Err(); err != nil {
		return policytest.RunResult{}, fmt.Errorf("%w: read policy test outcomes", model.ErrUnavailable)
	}
	if len(results) == 0 {
		status := policytest.StatusFail
		if run.State == policytest.StatePassed {
			status = policytest.StatusPass
		}
		return policytest.RunResult{Cases: append([]policytest.TestCaseResult(nil), run.Cases...), Status: status}, nil
	}
	status := policytest.StatusPass
	for _, result := range results {
		switch result.Result.Status {
		case policytest.StatusError:
			status = policytest.StatusError
		case policytest.StatusFail:
			if status == policytest.StatusPass {
				status = policytest.StatusFail
			}
		case policytest.StatusSkip:
			if status == policytest.StatusPass {
				status = policytest.StatusSkip
			}
		}
	}
	return policytest.RunResult{Cases: results, Status: status}, nil
}
