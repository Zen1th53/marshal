package authz

import (
	"context"
	"errors"
	"time"

	"github.com/Zen1th53/marshal/internal/evidence"
)

// CanObserved evaluates the canonical role decision and records a bounded,
// non-authoritative operational projection. Metrics are never consulted by
// the decision and cannot change its result.
func CanObserved(ctx context.Context, subject Principal, authority Authority, resource string, metrics *evidence.MetricsRecorder) (Decision, error) {
	started := time.Now()
	decision, err := Can(ctx, subject, authority, resource)
	if metrics != nil && ctx.Err() == nil {
		result := evidence.MetricResultSuccess
		if err != nil || !decision.Allowed {
			result = metricResult(decision, err)
		}
		metrics.Observe(evidence.MetricOperationAuthority, result, string(decision.Reason), time.Since(started))
	}
	return decision, err
}

func metricResult(decision Decision, err error) evidence.MetricResult {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return evidence.MetricResultCancelled
	}
	if decision.Reason == CodeRoleInvalid || decision.Reason == CodeUnknownRole || decision.Reason == CodeUnknownAuthority {
		return evidence.MetricResultInvalid
	}
	if err != nil || !decision.Allowed {
		return evidence.MetricResultDenied
	}
	return evidence.MetricResultSuccess
}
