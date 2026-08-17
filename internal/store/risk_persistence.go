package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/risk"
)

type riskRequirements struct {
	Authorities  []string `json:"authorities,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

func (s *Store) PutRiskAssessment(ctx context.Context, assessment risk.Assessment) error {
	if err := assessment.Validate(); err != nil {
		return err
	}
	factors, err := json.Marshal(assessment.Factors)
	if err != nil {
		return fmt.Errorf("%w: encode risk factors", model.ErrInvalid)
	}
	requirements, err := json.Marshal(riskRequirements{
		Authorities:  append([]string(nil), assessment.RequiredAuthorities...),
		Capabilities: append([]string(nil), assessment.RequiredCapabilities...),
	})
	if err != nil {
		return fmt.Errorf("%w: encode risk requirements", model.ErrInvalid)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO risk_assessments(
			assessment_id, action_digest, level, score, factors_json,
			requirements_json, policy_digest, state, created_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, string(assessment.ID), string(assessment.ActionDigest), string(assessment.Level), assessment.Score,
		string(factors), string(requirements), string(assessment.PolicyDigest), string(assessment.State), utcNow())
	if err == nil {
		return nil
	}
	if !strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
		return fmt.Errorf("persist risk assessment: %w", err)
	}
	stored, getErr := s.GetRiskAssessment(ctx, assessment.ID)
	if getErr != nil {
		return fmt.Errorf("read existing risk assessment: %w", getErr)
	}
	if riskAssessmentsEqual(stored, assessment) {
		return nil
	}
	return fmt.Errorf("%w: risk assessment is immutable", model.ErrConflict)
}

func (s *Store) GetRiskAssessment(ctx context.Context, id risk.AssessmentID) (risk.Assessment, error) {
	if strings.TrimSpace(string(id)) == "" {
		return risk.Assessment{}, fmt.Errorf("%w: assessment ID is required", model.ErrInvalid)
	}
	var assessment risk.Assessment
	var factorsJSON, requirementsJSON string
	err := s.db.QueryRowContext(ctx, `
		SELECT assessment_id, action_digest, level, score, factors_json,
		       requirements_json, policy_digest, state
		FROM risk_assessments WHERE assessment_id = ?
	`, string(id)).Scan(&assessment.ID, &assessment.ActionDigest, &assessment.Level, &assessment.Score,
		&factorsJSON, &requirementsJSON, &assessment.PolicyDigest, &assessment.State)
	if errors.Is(err, sql.ErrNoRows) {
		return risk.Assessment{}, fmt.Errorf("%w: risk assessment not found", model.ErrNotFound)
	}
	if err != nil {
		return risk.Assessment{}, fmt.Errorf("read risk assessment: %w", err)
	}
	if err := json.Unmarshal([]byte(factorsJSON), &assessment.Factors); err != nil {
		return risk.Assessment{}, fmt.Errorf("%w: invalid risk factors", model.ErrInvalid)
	}
	var requirements riskRequirements
	if err := json.Unmarshal([]byte(requirementsJSON), &requirements); err != nil {
		return risk.Assessment{}, fmt.Errorf("%w: invalid risk requirements", model.ErrInvalid)
	}
	assessment.RequiredAuthorities = requirements.Authorities
	assessment.RequiredCapabilities = requirements.Capabilities
	if err := assessment.Validate(); err != nil {
		return risk.Assessment{}, fmt.Errorf("%w: invalid durable risk assessment", model.ErrInvalid)
	}
	return assessment, nil
}

func (s *Store) TransitionRiskAssessmentState(ctx context.Context, id risk.AssessmentID, from, to risk.AssessmentState) error {
	if err := risk.ValidateState(from); err != nil {
		return err
	}
	if err := risk.ValidateState(to); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE risk_assessments SET state = ? WHERE assessment_id = ? AND state = ?
	`, string(to), string(id), string(from))
	if err != nil {
		return fmt.Errorf("transition risk assessment state: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return fmt.Errorf("%w: risk assessment state transition conflict", model.ErrConflict)
	}
	return nil
}

func riskAssessmentsEqual(left, right risk.Assessment) bool {
	return left.ID == right.ID && left.ActionDigest == right.ActionDigest && left.Level == right.Level &&
		left.Score == right.Score && left.Factors == right.Factors && left.PolicyDigest == right.PolicyDigest &&
		sameStrings(left.RequiredAuthorities, right.RequiredAuthorities) && sameStrings(left.RequiredCapabilities, right.RequiredCapabilities)
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
