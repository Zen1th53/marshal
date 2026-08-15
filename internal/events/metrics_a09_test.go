package events

import (
	"context"
	"testing"
)

func TestT43A09MetricsAreBoundedDetachedAndDoNotInfluenceOrdering(t *testing.T) {
	store := &a02EngineStore{}
	bus := &a02EngineBus{store: store}
	engine, err := NewEngine(store, bus)
	if err != nil {
		t.Fatal(err)
	}
	// A09 metrics must observe service calls even when A04 authority is absent;
	// they cannot make an unauthorized call succeed.
	_, _ = engine.Process(context.Background(), Event{ID: "EVENT-A09", Type: "events.appended", Subject: "system", IdempotencyKey: "REQ-A09"})
	snap := engine.Metrics()
	if snap.Observations[MetricOperationProcess] != 1 {
		t.Fatalf("process observations=%d", snap.Observations[MetricOperationProcess])
	}
	snap.Observations[MetricOperationProcess] = 999
	if engine.Metrics().Observations[MetricOperationProcess] != 1 {
		t.Fatal("snapshot aliases recorder")
	}
	if store.called {
		t.Fatal("metrics bypassed authorization")
	}
}

func TestT43A09PublishOutcomeUsesClosedDimensions(t *testing.T) {
	recorder := NewMetricsRecorder()
	recorder.Observe(MetricOperationPublish, MetricOutcomeDropped, 1)
	recorder.Observe(MetricOperation("attacker-id"), MetricOutcome("secret-label"), 1)
	snap := recorder.Snapshot()
	if snap.Observations[MetricOperationPublish] != 1 || snap.Outcomes[MetricOutcomeDropped] != 1 {
		t.Fatalf("snapshot=%+v", snap)
	}
	if len(snap.Observations) != 1 || len(snap.Outcomes) != 1 {
		t.Fatal("unbounded dimensions recorded")
	}
}
