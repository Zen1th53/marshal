package metrics_test

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/metrics"
)

func TestT132MemoryHealthAndObservability(t *testing.T) {
	c := metrics.NewCollector()
	ctx := context.Background()

	// 1. All healthy -> READY
	hReady := c.EvaluateHealth(ctx, metrics.HealthInputs{
		CanonicalDBHealthy: true,
		LexicalHealthy:     true,
		VectorHealthy:      true,
		GraphHealthy:       true,
		OutboxLag:          0,
	})
	if hReady.State != metrics.StatusReady {
		t.Fatalf("expected StatusReady, got: %s", hReady.State)
	}

	// 2. Vector index offline -> DEGRADED (NOT FAILED)
	hDegraded := c.EvaluateHealth(ctx, metrics.HealthInputs{
		CanonicalDBHealthy: true,
		LexicalHealthy:     true,
		VectorHealthy:      false, // Offline
		GraphHealthy:       true,
		OutboxLag:          5,
	})
	if hDegraded.State != metrics.StatusDegraded {
		t.Fatalf("expected StatusDegraded when vector index is offline, got: %s", hDegraded.State)
	}

	// 3. Canonical DB down -> FAILED
	hFailed := c.EvaluateHealth(ctx, metrics.HealthInputs{
		CanonicalDBHealthy: false,
		LexicalHealthy:     true,
		VectorHealthy:      true,
		GraphHealthy:       true,
	})
	if hFailed.State != metrics.StatusFailed {
		t.Fatalf("expected StatusFailed when canonical DB is down, got: %s", hFailed.State)
	}

	// 4. Metric Snapshot cardinality
	snap := c.Snapshot()
	if snap.Timestamp.IsZero() {
		t.Fatal("expected non-zero snapshot timestamp")
	}
}
