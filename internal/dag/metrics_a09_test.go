package dag

import (
	"context"
	"testing"
)

func TestT29A09MetricsAreBoundedAndDetached(t *testing.T) {
	backend := &a04Backend{}
	engine, err := NewAuthorizedEngine(backend, a04IdentityProvider{identity: a04Identity()}, a04Authorizer{decide: a04Allow}, FreshnessValidatorFunc(func(context.Context, AuthorizationRequest, AuthorizationDecision) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.AddNode(context.Background(), AddNodeRequest{RequestID: "REQ-A09", Node: Node{TaskID: "TASK-A", Kind: NodeKindTask, Status: StatusPending}}); err != nil {
		t.Fatal(err)
	}
	snapshot := engine.Metrics()
	if snapshot.Success != 1 || snapshot.TotalNs == 0 {
		t.Fatalf("metrics=%+v", snapshot)
	}
	if _, err := engine.AddNode(context.Background(), AddNodeRequest{RequestID: "REQ-A09-INVALID", Node: Node{TaskID: "bad", Kind: NodeKindTask, Status: StatusPending}}); err == nil {
		t.Fatal("invalid node accepted")
	}
	if engine.Metrics().Invalid == 0 {
		t.Fatal("invalid request was not counted")
	}
}

func BenchmarkT29DAGNodeValidation(b *testing.B) {
	node := Node{TaskID: "TASK-BENCH", Kind: NodeKindTask, Status: StatusPending}
	for i := 0; i < b.N; i++ {
		_ = node.Validate()
	}
}
