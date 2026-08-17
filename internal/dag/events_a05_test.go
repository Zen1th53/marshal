package dag

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
)

type a05Backend struct {
	order []string
	nodes map[TaskID]Node
	edges []Edge
}

func newA05Backend() *a05Backend { return &a05Backend{nodes: map[TaskID]Node{}} }
func (b *a05Backend) PutDAGNode(_ context.Context, n Node) (Node, error) {
	if existing, ok := b.nodes[n.TaskID]; ok {
		if existing == n {
			return existing, nil
		}
		return Node{}, ErrInvalidNode
	}
	b.order = append(b.order, "mutation")
	b.nodes[n.TaskID] = n
	return n, nil
}
func (b *a05Backend) GetDAGNode(_ context.Context, id TaskID) (Node, error) {
	n, ok := b.nodes[id]
	if !ok {
		return Node{}, ErrNodeNotFound
	}
	return n, nil
}
func (b *a05Backend) PutDAGEdge(_ context.Context, e Edge) (Edge, error) {
	b.order = append(b.order, "mutation")
	b.edges = append(b.edges, e)
	return e, nil
}
func (b *a05Backend) DAGEdgesFrom(_ context.Context, id TaskID) ([]Edge, error) {
	out := []Edge{}
	for _, e := range b.edges {
		if e.From == id {
			out = append(out, e)
		}
	}
	return out, nil
}
func (b *a05Backend) DAGEdgesTo(_ context.Context, id TaskID) ([]Edge, error) {
	out := []Edge{}
	for _, e := range b.edges {
		if e.To == id {
			out = append(out, e)
		}
	}
	return out, nil
}
func (b *a05Backend) DAGNodes(context.Context) ([]Node, error) {
	out := make([]Node, 0, len(b.nodes))
	for _, n := range b.nodes {
		out = append(out, n)
	}
	return out, nil
}
func (b *a05Backend) TransitionDAGNode(_ context.Context, id TaskID, expected, target NodeStatus) (Node, error) {
	n, ok := b.nodes[id]
	if !ok {
		return Node{}, ErrNodeNotFound
	}
	if n.Status == target {
		return n, nil
	}
	if n.Status != expected {
		return Node{}, ErrInvalidNode
	}
	b.order = append(b.order, "mutation")
	n.Status = target
	b.nodes[id] = n
	return n, nil
}

type a05EventSink struct {
	order *[]string
	items []events.Event
	err   error
}

func (s *a05EventSink) Append(_ context.Context, e events.Event) (events.Event, error) {
	if s.order != nil {
		*s.order = append(*s.order, "event")
	}
	if s.err != nil {
		return events.Event{}, s.err
	}
	for _, old := range s.items {
		if old.IdempotencyKey == e.IdempotencyKey {
			if old.ID == e.ID && old.Type == e.Type {
				return old, nil
			}
			return events.Event{}, events.ErrSequenceConflict
		}
	}
	e.Sequence = events.Sequence(len(s.items) + 1)
	e.At = time.Now().UTC()
	s.items = append(s.items, events.CloneEvent(e))
	return e, nil
}

func a05Engine(t *testing.T, backend Backend, sink EventSink) *Engine {
	t.Helper()
	engine, err := NewAuditedEngine(backend, a04IdentityProvider{identity: a04Identity()}, a04Authorizer{decide: a04Allow}, FreshnessValidatorFunc(func(context.Context, AuthorizationRequest, AuthorizationDecision) error { return nil }), sink)
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func TestT29A05AddNodeEmitsDurableEventAfterMutation(t *testing.T) {
	backend := newA05Backend()
	sink := &a05EventSink{order: &backend.order}
	engine := a05Engine(t, backend, sink)
	_, err := engine.AddNode(context.Background(), AddNodeRequest{RequestID: "REQ-A05-NODE", Node: Node{TaskID: "TASK-NODE", Kind: NodeKindTask, Status: StatusPending}})
	if err != nil {
		t.Fatal(err)
	}
	if len(sink.items) != 1 || sink.items[0].Type != "dag.node.added" {
		t.Fatalf("events=%+v", sink.items)
	}
	if got := backend.order; len(got) != 2 || got[0] != "mutation" || got[1] != "event" {
		t.Fatalf("order=%v", got)
	}
	ev := sink.items[0]
	if err := ev.Validate(); err != nil {
		t.Fatalf("generated event invalid: %v", err)
	}
	if ev.Subject != "SUBJECT-A04" || ev.TaskID != "TASK-CALLER" || ev.ResourceID != "TASK-NODE" {
		t.Fatalf("event identity=%+v", ev)
	}
	if ev.Data["change_id"] != "CHANGE-A04" || ev.Data["policy_digest"] == "" || ev.Data["result"] != "added" {
		t.Fatalf("data=%v", ev.Data)
	}
}

func TestT29A05MutationFailsClosedWithoutEventSink(t *testing.T) {
	backend := newA05Backend()
	engine, err := NewAuthorizedEngine(backend, a04IdentityProvider{identity: a04Identity()}, a04Authorizer{decide: a04Allow}, FreshnessValidatorFunc(func(context.Context, AuthorizationRequest, AuthorizationDecision) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.AddNode(context.Background(), AddNodeRequest{RequestID: "REQ-A05-NOSINK", Node: Node{TaskID: "TASK-NOSINK", Kind: NodeKindTask, Status: StatusPending}})
	if !errors.Is(err, ErrEventUnavailable) {
		t.Fatalf("error=%v", err)
	}
	if len(backend.nodes) != 0 {
		t.Fatalf("nodes=%v", backend.nodes)
	}
}

func TestT29A05CycleRejectionEmitsOneDenyEventWithoutMutation(t *testing.T) {
	backend := newA05Backend()
	backend.nodes["TASK-A"] = Node{TaskID: "TASK-A", Kind: NodeKindTask, Status: StatusPending}
	backend.nodes["TASK-B"] = Node{TaskID: "TASK-B", Kind: NodeKindTask, Status: StatusPending}
	backend.edges = []Edge{{From: "TASK-A", To: "TASK-B", Condition: ConditionCompleted}}
	sink := &a05EventSink{}
	engine := a05Engine(t, backend, sink)
	_, err := engine.AddEdge(context.Background(), AddEdgeRequest{RequestID: "REQ-A05-CYCLE", Edge: Edge{From: "TASK-B", To: "TASK-A", Condition: ConditionCompleted}})
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("error=%v", err)
	}
	if len(backend.edges) != 1 {
		t.Fatalf("edges=%v", backend.edges)
	}
	if len(sink.items) != 1 || sink.items[0].Type != "dag.cycle.rejected" || sink.items[0].Data["reason_code"] != string(CodeCycle) {
		t.Fatalf("events=%+v", sink.items)
	}
}

func TestT29A05ReadyAndBlockedTransitionsEmitCanonicalEvents(t *testing.T) {
	backend := newA05Backend()
	backend.nodes["TASK-READY"] = Node{TaskID: "TASK-READY", Kind: NodeKindTask, Status: StatusPending}
	backend.nodes["TASK-BLOCKED"] = Node{TaskID: "TASK-BLOCKED", Kind: NodeKindTask, Status: StatusRunning}
	sink := &a05EventSink{}
	engine := a05Engine(t, backend, sink)
	if _, err := engine.Transition(context.Background(), "TASK-READY", StatusPending, StatusReady); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Transition(context.Background(), "TASK-BLOCKED", StatusRunning, StatusBlocked); err != nil {
		t.Fatal(err)
	}
	if len(sink.items) != 2 || sink.items[0].Type != "dag.node.ready" || sink.items[1].Type != "dag.node.blocked" {
		t.Fatalf("events=%+v", sink.items)
	}
}

func TestT29A05EventFailureAfterCommitReconcilesOnExactRetry(t *testing.T) {
	backend := newA05Backend()
	marker := errors.New("MARSHAL_TEST_SECRET_T29_A05_41bf")
	sink := &a05EventSink{err: marker}
	engine := a05Engine(t, backend, sink)
	request := AddNodeRequest{RequestID: "REQ-A05-RETRY", Node: Node{TaskID: "TASK-RETRY", Kind: NodeKindTask, Status: StatusPending}}
	stored, err := engine.AddNode(context.Background(), request)
	if !errors.Is(err, ErrEventUnavailable) || stored.TaskID != "TASK-RETRY" {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
	if strings.Contains(err.Error(), marker.Error()) {
		t.Fatalf("secret leaked: %q", err.Error())
	}
	sink.err = nil
	if _, err := engine.AddNode(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if len(sink.items) != 1 || sink.items[0].Type != "dag.node.added" {
		t.Fatalf("events=%+v", sink.items)
	}
	if got := len(backend.order); got != 1 {
		t.Fatalf("canonical mutation count=%d want=1", got)
	}
}
