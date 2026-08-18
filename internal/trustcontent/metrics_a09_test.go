package trustcontent

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/evidence"
)

func TestEngineRecordsBoundedIngestMetrics(t *testing.T) {
	metrics := evidence.NewMetricsRecorder()
	engine := NewEngine(EngineConfig{
		Repository: &memoryRepository{}, Sanitizer: evidence.NewStrictSanitizer(evidence.SanitizerConfig{}),
		Authorizer: allowingAuthorizer{}, Metrics: metrics,
	})
	if _, err := engine.Ingest(context.Background(), IngestRequest{ID: "segment-metrics", IdempotencyKey: "request-metrics", Source: SourceRepository, SourceID: "repo/metrics", Content: "data"}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	snapshot := metrics.Snapshot()
	if snapshot.Success[evidence.MetricOperationTrustContent] != 1 || snapshot.Observations[evidence.MetricOperationTrustContent] != 1 {
		t.Fatalf("metrics = %#v", snapshot)
	}
}
