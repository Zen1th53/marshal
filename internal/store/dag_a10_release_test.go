package store

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/dag"
	"github.com/Zen1th53/marshal/internal/events"
)

func t29A10Engine(t *testing.T, st *Store) *dag.Engine {
	t.Helper()
	identity := dag.Identity{SubjectID: "SUBJECT-A10", SessionID: "SESSION-A10", TaskID: "TASK-CALLER", ChangeID: "CHANGE-A10"}
	engine, err := dag.NewAuditedEngine(
		st,
		dag.IdentityProviderFunc(func(context.Context) (dag.Identity, error) { return identity, nil }),
		dag.AuthorizerFunc(func(_ context.Context, r dag.AuthorizationRequest) (dag.AuthorizationDecision, error) {
			return dag.AuthorizationDecision{
				Allowed: true, Identity: r.Identity, RequestID: r.RequestID, Action: r.Action,
				Resource: r.Resource, ExpectedState: r.ExpectedState, TargetState: r.TargetState,
				PolicyDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				FreshUntil:   time.Now().Add(time.Hour),
			}, nil
		}),
		dag.FreshnessValidatorFunc(func(context.Context, dag.AuthorizationRequest, dag.AuthorizationDecision) error { return nil }),
		dag.EventSinkFunc(func(_ context.Context, event events.Event) (events.Event, error) {
			if err := event.Validate(); err != nil {
				return events.Event{}, err
			}
			return event, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func TestT29A10ReleaseFlowSurvivesRestartAndKeepsStableReadiness(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/dag-a10.db"
	st, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		st.Close()
		t.Fatal(err)
	}
	engine := t29A10Engine(t, st)
	for _, id := range []dag.TaskID{"TASK-A10-A", "TASK-A10-B"} {
		if _, err := engine.AddNode(ctx, dag.AddNodeRequest{RequestID: dag.RequestID("REQ-" + string(id)), Node: dag.Node{TaskID: id, Kind: dag.NodeKindTask, Status: dag.StatusPending}}); err != nil {
			t.Fatal(err)
		}
	}
	edge := dag.Edge{From: "TASK-A10-A", To: "TASK-A10-B", Condition: dag.ConditionCompleted}
	if _, err := engine.AddEdge(ctx, dag.AddEdgeRequest{RequestID: "REQ-A10-EDGE", Edge: edge}); err != nil {
		t.Fatal(err)
	}
	for _, transition := range [][2]dag.NodeStatus{{dag.StatusPending, dag.StatusReady}, {dag.StatusReady, dag.StatusRunning}, {dag.StatusRunning, dag.StatusCompleted}} {
		if _, err := engine.Transition(ctx, "TASK-A10-A", transition[0], transition[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	engine = t29A10Engine(t, reopened)
	ready, err := engine.Ready(ctx, "TASK-A10-B")
	if err != nil {
		t.Fatal(err)
	}
	if !ready.Ready || len(ready.BlockedBy) != 0 {
		t.Fatalf("readiness=%+v", ready)
	}
	if _, err := engine.Transition(ctx, "TASK-A10-B", dag.StatusPending, dag.StatusReady); err != nil {
		t.Fatal(err)
	}
	order, err := engine.Topological(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0].TaskID != "TASK-A10-A" || order[1].TaskID != "TASK-A10-B" {
		t.Fatalf("order=%+v", order)
	}
	if _, err := engine.AddEdge(ctx, dag.AddEdgeRequest{RequestID: "REQ-A10-EDGE", Edge: edge}); err != nil {
		t.Fatalf("exact retry: %v", err)
	}
}
