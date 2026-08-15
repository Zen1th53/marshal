package dag

import (
	"sync"
	"time"
)

type MetricOperation string
type MetricOutcome string

const (
	MetricOperationAddEdge     MetricOperation = "add_edge"
	MetricOperationReady       MetricOperation = "ready"
	MetricOperationTopological MetricOperation = "topological"
	MetricOutcomeSuccess       MetricOutcome   = "success"
	MetricOutcomeBlocked       MetricOutcome   = "blocked"
	MetricOutcomeCycleRejected MetricOutcome   = "cycle_rejected"
	MetricOutcomeError         MetricOutcome   = "error"
)

var dagMetricOperations = map[MetricOperation]struct{}{MetricOperationAddEdge: {}, MetricOperationReady: {}, MetricOperationTopological: {}}
var dagMetricOutcomes = map[MetricOutcome]struct{}{MetricOutcomeSuccess: {}, MetricOutcomeBlocked: {}, MetricOutcomeCycleRejected: {}, MetricOutcomeError: {}}

type MetricsSnapshot struct {
	Observations        map[MetricOperation]uint64
	Outcomes            map[MetricOutcome]uint64
	DurationNanoseconds map[MetricOperation]uint64
}

type MetricsRecorder struct {
	mu       sync.RWMutex
	snapshot MetricsSnapshot
}

func NewMetricsRecorder() *MetricsRecorder {
	return &MetricsRecorder{snapshot: MetricsSnapshot{Observations: map[MetricOperation]uint64{}, Outcomes: map[MetricOutcome]uint64{}, DurationNanoseconds: map[MetricOperation]uint64{}}}
}
func (r *MetricsRecorder) Observe(op MetricOperation, out MetricOutcome, d time.Duration) {
	if r == nil {
		return
	}
	if _, ok := dagMetricOperations[op]; !ok {
		return
	}
	if _, ok := dagMetricOutcomes[out]; !ok {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.snapshot.Observations[op]++
	r.snapshot.Outcomes[out]++
	if d > 0 {
		r.snapshot.DurationNanoseconds[op] += uint64(d)
	}
}
func (r *MetricsRecorder) Snapshot() MetricsSnapshot {
	if r == nil {
		return MetricsSnapshot{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	o := MetricsSnapshot{Observations: map[MetricOperation]uint64{}, Outcomes: map[MetricOutcome]uint64{}, DurationNanoseconds: map[MetricOperation]uint64{}}
	for k, v := range r.snapshot.Observations {
		o.Observations[k] = v
	}
	for k, v := range r.snapshot.Outcomes {
		o.Outcomes[k] = v
	}
	for k, v := range r.snapshot.DurationNanoseconds {
		o.DurationNanoseconds[k] = v
	}
	return o
}
