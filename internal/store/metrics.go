package store

import (
	"errors"
	"time"

	"github.com/Zen1th53/marshal/internal/evidence"
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
