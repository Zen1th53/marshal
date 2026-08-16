package evidence

import (
	"context"
	"sync"
	"time"
)

// MetricOperation is the closed set of T06 operations exposed to operators.
// It intentionally excludes resource identifiers and provider output.
type MetricOperation string

const (
	MetricOperationPutNode           MetricOperation = "put_node"
	MetricOperationGet               MetricOperation = "get"
	MetricOperationLink              MetricOperation = "link"
	MetricOperationNeighbors         MetricOperation = "neighbors"
	MetricOperationTransition        MetricOperation = "transition"
	MetricOperationSanitize          MetricOperation = "sanitize"
	MetricOperationDigest            MetricOperation = "digest"
	MetricOperationAudit             MetricOperation = "audit"
	MetricOperationFreshness         MetricOperation = "freshness"
	MetricOperationContention        MetricOperation = "contention"
	MetricOperationPolicyLoad        MetricOperation = "policy_load"
	MetricOperationPolicyPersist     MetricOperation = "policy_persist"
	MetricOperationPolicyTransition  MetricOperation = "policy_transition"
	MetricOperationPolicyRuntimeGate MetricOperation = "policy_runtime_gate"
	MetricOperationPolicyTest        MetricOperation = "policy_test"
	MetricOperationCapability        MetricOperation = "capability"
	MetricOperationAuthority         MetricOperation = "authority"
	MetricOperationGate              MetricOperation = "gate"
	MetricOperationSecret            MetricOperation = "secret"
)

// MetricResult is a bounded outcome dimension.
type MetricResult string

const (
	MetricResultSuccess   MetricResult = "success"
	MetricResultDenied    MetricResult = "denied"
	MetricResultInvalid   MetricResult = "invalid"
	MetricResultConflict  MetricResult = "conflict"
	MetricResultError     MetricResult = "error"
	MetricResultCancelled MetricResult = "cancelled"
)

var metricOperations = map[MetricOperation]struct{}{
	MetricOperationPutNode: {}, MetricOperationGet: {}, MetricOperationLink: {},
	MetricOperationNeighbors: {}, MetricOperationTransition: {}, MetricOperationSanitize: {},
	MetricOperationDigest: {}, MetricOperationAudit: {}, MetricOperationFreshness: {},
	MetricOperationContention: {}, MetricOperationPolicyLoad: {}, MetricOperationPolicyPersist: {},
	MetricOperationPolicyTransition: {}, MetricOperationPolicyRuntimeGate: {},
	MetricOperationPolicyTest: {}, MetricOperationCapability: {}, MetricOperationAuthority: {}, MetricOperationGate: {}, MetricOperationSecret: {},
}

var metricResults = map[MetricResult]struct{}{
	MetricResultSuccess: {}, MetricResultDenied: {}, MetricResultInvalid: {},
	MetricResultConflict: {}, MetricResultError: {}, MetricResultCancelled: {},
}

var metricReasons = map[string]struct{}{
	string(CodeAuthorizationDenied): {}, string(CodeAuthorizationStale): {},
	string(CodeAuthorizationUnavailable): {}, string(CodeInvalidType): {},
	string(CodeDigestMismatch): {}, string(CodeImmutable): {},
	string(CodeInvalidEdge): {}, string(CodeSecretRejected): {},
	string(CodeInvalidState): {}, "SQLITE_BUSY": {}, "SQLITE_RETRY_EXHAUSTED": {},
	"POLICY_CONFLICT": {}, "POLICY_DENIED": {}, "POLICY_ERROR": {},
	"CAP_DENIED": {}, "CAP_INVALID_SCOPE": {}, "CAP_EXPIRED": {},
	"CAP_REVOKED": {}, "CAP_SUBJECT_MISMATCH": {}, "CAP_TASK_MISMATCH": {},
	"GATE_ALLOWED": {}, "GATE_REQUIRED_CHECK_MISSING": {},
	"GATE_POLICY_DENY": {}, "GATE_QUORUM_UNMET": {}, "GATE_UNKNOWN_CHECK": {},
	"GATE_UNKNOWN_POINT": {}, "GATE_INVALID_CHECK_STATUS": {}, "GATE_INVALID_DECISION": {},
	"SECRET_DENIED": {}, "SECRET_NOT_FOUND": {}, "SECRET_LEASE_EXPIRED": {}, "SECRET_PURPOSE_MISMATCH": {}, "SECRET_PROVIDER_FAILED": {},
}

// MetricsSnapshot is a detached, read-only projection of recorder state.
type MetricsSnapshot struct {
	Success             map[MetricOperation]uint64
	Active              map[MetricOperation]uint64
	Denied              map[string]uint64
	Invalid             map[string]uint64
	Conflict            map[string]uint64
	Errors              map[string]uint64
	Cancelled           map[MetricOperation]uint64
	Observations        map[MetricOperation]uint64
	DurationNanoseconds map[MetricOperation]uint64
	LastFailureReason   string
}

// MetricsRecorder is a small in-process operational projection. It is not
// persisted and never participates in authorization or evidence mutation.
type MetricsRecorder struct {
	mu       sync.RWMutex
	snapshot MetricsSnapshot
}

func NewMetricsRecorder() *MetricsRecorder {
	return &MetricsRecorder{snapshot: MetricsSnapshot{
		Success: make(map[MetricOperation]uint64), Active: make(map[MetricOperation]uint64), Denied: make(map[string]uint64),
		Invalid: make(map[string]uint64), Conflict: make(map[string]uint64),
		Errors: make(map[string]uint64), Cancelled: make(map[MetricOperation]uint64),
		Observations: make(map[MetricOperation]uint64), DurationNanoseconds: make(map[MetricOperation]uint64),
	}}
}

// AddActive records a bounded, non-authoritative count for an owned active
// object such as a policy-test execution claim.
func (r *MetricsRecorder) AddActive(operation MetricOperation, delta int64) {
	if r == nil || !validMetricOperation(operation) {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	current := r.snapshot.Active[operation]
	if delta < 0 {
		decrement := uint64(-delta)
		if decrement >= current {
			r.snapshot.Active[operation] = 0
			return
		}
		r.snapshot.Active[operation] = current - decrement
		return
	}
	r.snapshot.Active[operation] = current + uint64(delta)
}

// Observe records only closed vocabulary values. Unknown operation/result or
// reason values are discarded to prevent attacker-controlled cardinality.
func (r *MetricsRecorder) Observe(operation MetricOperation, result MetricResult, reason string, duration time.Duration) {
	if r == nil || !validMetricOperation(operation) || !validMetricResult(result) {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.snapshot.Observations[operation]++
	if duration > 0 {
		r.snapshot.DurationNanoseconds[operation] += uint64(duration)
	}
	safeReason := canonicalMetricReason(reason)
	switch result {
	case MetricResultSuccess:
		r.snapshot.Success[operation]++
	case MetricResultDenied:
		r.snapshot.Denied[safeReason]++
		r.snapshot.LastFailureReason = safeReason
	case MetricResultInvalid:
		r.snapshot.Invalid[safeReason]++
		r.snapshot.LastFailureReason = safeReason
	case MetricResultConflict:
		r.snapshot.Conflict[safeReason]++
		r.snapshot.LastFailureReason = safeReason
	case MetricResultError:
		r.snapshot.Errors[safeReason]++
		r.snapshot.LastFailureReason = safeReason
	case MetricResultCancelled:
		r.snapshot.Cancelled[operation]++
		r.snapshot.LastFailureReason = safeReason
	}
}

// ObserveContext avoids recording work that was cancelled before it began.
func (r *MetricsRecorder) ObserveContext(ctx context.Context, operation MetricOperation, result MetricResult, reason string, duration time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.Observe(operation, result, reason, duration)
	return nil
}

func (r *MetricsRecorder) Snapshot() MetricsSnapshot {
	if r == nil {
		return MetricsSnapshot{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := r.snapshot
	out.Success = cloneMetricOperations(r.snapshot.Success)
	out.Active = cloneMetricOperations(r.snapshot.Active)
	out.Cancelled = cloneMetricOperations(r.snapshot.Cancelled)
	out.Observations = cloneMetricOperations(r.snapshot.Observations)
	out.DurationNanoseconds = cloneMetricOperations(r.snapshot.DurationNanoseconds)
	out.Denied = cloneMetricReasons(r.snapshot.Denied)
	out.Invalid = cloneMetricReasons(r.snapshot.Invalid)
	out.Conflict = cloneMetricReasons(r.snapshot.Conflict)
	out.Errors = cloneMetricReasons(r.snapshot.Errors)
	return out
}

func validMetricOperation(operation MetricOperation) bool {
	_, ok := metricOperations[operation]
	return ok
}
func validMetricResult(result MetricResult) bool { _, ok := metricResults[result]; return ok }
func canonicalMetricReason(reason string) string {
	if _, ok := metricReasons[reason]; ok {
		return reason
	}
	return "UNCLASSIFIED"
}
func cloneMetricOperations(in map[MetricOperation]uint64) map[MetricOperation]uint64 {
	out := make(map[MetricOperation]uint64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
func cloneMetricReasons(in map[string]uint64) map[string]uint64 {
	out := make(map[string]uint64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
