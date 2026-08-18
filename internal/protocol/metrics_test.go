package protocol

import (
	"context"
	"testing"
)

func TestA09MetricsExposeBoundedHandoffOutcomesAndActiveCount(t *testing.T) {
	metrics := NewMetrics()
	repository := &memoryRepository{}
	service := NewService(Config{Metrics: metrics}, repository, allowAuthorizer{})
	if _, err := service.Submit(context.Background(), Principal{ID: "AGENT-developer", Role: RoleDeveloper}, validSubmission()); err != nil {
		t.Fatal(err)
	}
	forged := validSubmission()
	forged.Handoff.FromAgent = "AGENT-forged"
	if _, err := service.Submit(context.Background(), Principal{ID: "AGENT-developer", Role: RoleDeveloper}, forged); err == nil {
		t.Fatal("forged sender was accepted")
	}
	snapshot := metrics.Snapshot()
	if snapshot.Accepted != 1 || snapshot.Denied[CodeSenderForged] != 1 || snapshot.Active != 1 || snapshot.DurationNanos == 0 {
		t.Fatalf("metrics = %#v, want accepted, bounded denial, active count, and latency", snapshot)
	}
}
