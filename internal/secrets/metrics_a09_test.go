package secrets

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/evidence"
)

func TestEngineMetricsAreBoundedObservationalProjection(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	store := &memoryLeaseStore{}
	metrics := evidence.NewMetricsRecorder()
	engine, err := NewEngine(EngineConfig{
		Store: store, Capability: allowSecretCapability{}, Metrics: metrics,
		Providers: map[string]Provider{"env": providerFunc(func(context.Context, Ref) ([]byte, error) { return []byte("secret"), nil })},
		Now:       func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := engine.Lease(context.Background(), LeaseRequest{ID: "metrics", Subject: "agent", TaskID: "task", Ref: Ref{Provider: "env", Name: "TOKEN", Version: "v1"}, Purpose: "test", IssuedAt: now, ExpiresAt: now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.WithSecret(context.Background(), lease, func([]byte) error { return nil }); err != nil {
		t.Fatal(err)
	}
	snapshot := metrics.Snapshot()
	if snapshot.Success[evidence.MetricOperationSecret] != 1 || snapshot.Active[evidence.MetricOperationSecret] != 0 || snapshot.DurationNanoseconds[evidence.MetricOperationSecret] == 0 {
		t.Fatalf("metrics snapshot=%#v", snapshot)
	}

	deniedMetrics := evidence.NewMetricsRecorder()
	deniedEngine, err := NewEngine(EngineConfig{
		Store: store, Capability: denySecretCapability{}, Metrics: deniedMetrics,
		Providers: map[string]Provider{"env": providerFunc(func(context.Context, Ref) ([]byte, error) { t.Fatal("denied provider called"); return nil, nil })},
		Now:       func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := deniedEngine.WithSecret(context.Background(), lease, func([]byte) error { t.Fatal("denied callback called"); return nil }); err == nil {
		t.Fatal("denied secret access succeeded")
	}
	if deniedMetrics.Snapshot().Denied[string(CodeDenied)] == 0 {
		t.Fatalf("denial metric=%#v", deniedMetrics.Snapshot())
	}
}
