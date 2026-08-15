package dag

import (
	"context"
	"testing"
)

func TestT29A09MetricsAreBoundedDetachedAndNonAuthoritative(t *testing.T) {
	backend := newA05Backend()
	backend.nodes["TASK-A"] = Node{TaskID: "TASK-A", Kind: NodeKindTask, Status: StatusPending}
	backend.nodes["TASK-B"] = Node{TaskID: "TASK-B", Kind: NodeKindTask, Status: StatusPending}
	engine := a05Engine(t, backend, &a05EventSink{})

	before, err := engine.Ready(context.Background(), "TASK-B")
	if err != nil {
		t.Fatal(err)
	}
	snap := engine.Metrics()
	if snap.Observations[MetricOperationReady] != 1 {
		t.Fatalf("ready observations=%d", snap.Observations[MetricOperationReady])
	}
	snap.Observations[MetricOperationReady] = 999
	after, err := engine.Ready(context.Background(), "TASK-B")
	if err != nil {
		t.Fatal(err)
	}
	if before.Ready != after.Ready {
		t.Fatal("metrics influenced readiness")
	}
	if engine.Metrics().Observations[MetricOperationReady] != 2 {
		t.Fatal("snapshot aliases recorder")
	}
}

func TestT29A09CycleRejectUsesClosedOutcomeWithoutResourceLabels(t *testing.T) {
	backend := newA05Backend()
	backend.nodes["TASK-A"] = Node{TaskID: "TASK-A", Kind: NodeKindTask, Status: StatusPending}
	backend.nodes["TASK-B"] = Node{TaskID: "TASK-B", Kind: NodeKindTask, Status: StatusPending}
	backend.edges = []Edge{{From: "TASK-A", To: "TASK-B", Condition: ConditionCompleted}}
	engine := a05Engine(t, backend, &a05EventSink{})
	_, _ = engine.AddEdge(context.Background(), AddEdgeRequest{RequestID: "REQ-A09-CYCLE", Edge: Edge{From: "TASK-B", To: "TASK-A", Condition: ConditionCompleted}})
	snap := engine.Metrics()
	if snap.Outcomes[MetricOutcomeCycleRejected] != 1 {
		t.Fatalf("cycle rejects=%d", snap.Outcomes[MetricOutcomeCycleRejected])
	}
}
