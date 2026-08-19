package metrics

import (
	"context"
	"sync"
	"time"
)

type HealthState string

const (
	StatusReady    HealthState = "READY"
	StatusDegraded HealthState = "DEGRADED"
	StatusFailed   HealthState = "FAILED"
)

type HealthInputs struct {
	CanonicalDBHealthy bool  `json:"canonical_db_healthy"`
	LexicalHealthy     bool  `json:"lexical_healthy"`
	VectorHealthy      bool  `json:"vector_healthy"`
	GraphHealthy       bool  `json:"graph_healthy"`
	OutboxLag          int64 `json:"outbox_lag"`
}

type HealthReport struct {
	State            HealthState `json:"state"`
	DegradedChannels []string    `json:"degraded_channels,omitempty"`
	Message          string      `json:"message"`
}

type MetricSnapshot struct {
	OutboxLag          int64     `json:"outbox_lag"`
	CanonicalWatermark int64     `json:"canonical_watermark"`
	StalenessCount     int       `json:"staleness_count"`
	ConflictCount      int       `json:"conflict_count"`
	RetrievalLatencyMs float64   `json:"retrieval_latency_ms"`
	Timestamp          time.Time `json:"timestamp"`
}

type Collector struct {
	mu       sync.RWMutex
	snapshot MetricSnapshot
}

func NewCollector() *Collector {
	return &Collector{
		snapshot: MetricSnapshot{
			Timestamp: time.Now().UTC(),
		},
	}
}

// EvaluateHealth computes overall system readiness, distinguishing canonical failure from partial index degradation.
func (c *Collector) EvaluateHealth(ctx context.Context, inputs HealthInputs) HealthReport {
	if !inputs.CanonicalDBHealthy {
		return HealthReport{
			State:   StatusFailed,
			Message: "Canonical SQLite database is unavailable or corrupted",
		}
	}

	var degraded []string
	if !inputs.LexicalHealthy {
		degraded = append(degraded, "lexical")
	}
	if !inputs.VectorHealthy {
		degraded = append(degraded, "vector")
	}
	if !inputs.GraphHealthy {
		degraded = append(degraded, "graph")
	}
	if inputs.OutboxLag > 50 {
		degraded = append(degraded, "outbox_lag")
	}

	if len(degraded) > 0 {
		return HealthReport{
			State:            StatusDegraded,
			DegradedChannels: degraded,
			Message:          "One or more derived retrieval channels are operating in degraded mode",
		}
	}

	return HealthReport{
		State:   StatusReady,
		Message: "All canonical storage and derived indexes healthy",
	}
}

// RecordMetrics updates live operational telemetry metrics.
func (c *Collector) RecordMetrics(snap MetricSnapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	snap.Timestamp = time.Now().UTC()
	c.snapshot = snap
}

// Snapshot returns the current operational telemetry snapshot.
func (c *Collector) Snapshot() MetricSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snapshot
}
