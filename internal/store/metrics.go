package store

import (
	"errors"
	"time"

	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/policy"
	"github.com/Zen1th53/marshal/internal/policytest"
)

func (s *Store) observeMetric(operation evidence.MetricOperation, err error, started time.Time) {
	if s.metrics == nil {
		return
	}
	result := evidence.MetricResultSuccess
	reason := ""
	if err != nil {
		reason = string(evidence.ReasonCode(err))
		switch {
		case errors.Is(err, evidence.ErrAuthorizationDenied), errors.Is(err, evidence.ErrAuthorizationUnavailable), errors.Is(err, evidence.ErrAuthorizationStale):
			result = evidence.MetricResultDenied
		case errors.Is(err, evidence.ErrImmutable):
			result = evidence.MetricResultConflict
		case errors.Is(err, evidence.ErrInvalidType), errors.Is(err, evidence.ErrInvalidEdge), errors.Is(err, evidence.ErrInvalidTransition), errors.Is(err, evidence.ErrDigestMismatch), errors.Is(err, evidence.ErrSecretRejected):
			result = evidence.MetricResultInvalid
		default:
			result = evidence.MetricResultError
		}
	}
	s.metrics.Observe(operation, result, reason, time.Since(started))
}

func (s *Store) observeContention() {
	if s.metrics != nil {
		s.metrics.Observe(evidence.MetricOperationContention, evidence.MetricResultError, "SQLITE_BUSY", 0)
	}
}

func (s *Store) observePolicyMetric(operation evidence.MetricOperation, err error, started time.Time) {
	if s.metrics == nil {
		return
	}
	result := evidence.MetricResultSuccess
	reason := ""
	if err != nil {
		result = evidence.MetricResultError
		reason = "POLICY_ERROR"
		switch {
		case errors.Is(err, policy.ErrConflict), errors.Is(err, policy.ErrStaleBinding):
			result, reason = evidence.MetricResultConflict, "POLICY_CONFLICT"
		case errors.Is(err, policy.ErrDeny), errors.Is(err, policy.ErrAuthorizationDenied), errors.Is(err, policy.ErrAuthorizationUnavailable), errors.Is(err, policy.ErrAuthorizationStale):
			result, reason = evidence.MetricResultDenied, "POLICY_DENIED"
		}
	}
	s.metrics.Observe(operation, result, reason, time.Since(started))
}

func (s *Store) observePolicyTestMetric(result policytest.ResultStatus, err error, started time.Time) {
	if s.metrics == nil {
		return
	}
	metricResult := evidence.MetricResultSuccess
	reason := ""
	if err != nil {
		metricResult = evidence.MetricResultError
		reason = "POLICY_ERROR"
		switch {
		case errors.Is(err, policy.ErrAuthorizationDenied), errors.Is(err, policy.ErrAuthorizationStale), errors.Is(err, policy.ErrAuthorizationUnavailable):
			metricResult, reason = evidence.MetricResultDenied, "POLICY_DENIED"
		case errors.Is(err, policytest.ErrCaseInvalid), errors.Is(err, policy.ErrParseError), errors.Is(err, policy.ErrUnknownField), errors.Is(err, policy.ErrUnknownAction), errors.Is(err, policy.ErrAuthorizationInvalid):
			metricResult, reason = evidence.MetricResultInvalid, "POLICY_ERROR"
		}
	} else {
		switch result {
		case policytest.StatusFail:
			metricResult, reason = evidence.MetricResultDenied, "POLICY_DENIED"
		case policytest.StatusError:
			metricResult, reason = evidence.MetricResultError, "POLICY_ERROR"
		case policytest.StatusSkip:
			metricResult, reason = evidence.MetricResultInvalid, "POLICY_ERROR"
		}
	}
	s.metrics.Observe(evidence.MetricOperationPolicyTest, metricResult, reason, time.Since(started))
}

func (s *Store) adjustActivePolicyTestClaims(delta int64) {
	if s.metrics != nil {
		s.metrics.AddActive(evidence.MetricOperationPolicyTest, delta)
	}
}
