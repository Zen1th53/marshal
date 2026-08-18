package dag

import (
	"context"
	"github.com/Zen1th53/marshal/internal/events"
	"testing"
)

type a05EventStore struct{ events []events.Event }

func (s *a05EventStore) Append(_ context.Context, event events.Event) (events.Event, error) {
	if err := event.Validate(); err != nil {
		return events.Event{}, err
	}
	event.Sequence = events.Sequence(len(s.events) + 1)
	s.events = append(s.events, event)
	return event, nil
}
func (s *a05EventStore) Since(_ context.Context, after events.Sequence) ([]events.Event, error) {
	if int(after) >= len(s.events) {
		return nil, nil
	}
	return append([]events.Event(nil), s.events[int(after):]...), nil
}

func TestT29A05AuthorizedNodeMutationRecordsBoundedEvent(t *testing.T) {
	backend := &a04Backend{}
	history := &a05EventStore{}
	engine, err := NewAuthorizedEngineWithEvents(backend, a04IdentityProvider{identity: a04Identity()}, a04Authorizer{decide: a04Allow}, FreshnessValidatorFunc(func(context.Context, AuthorizationRequest, AuthorizationDecision) error { return nil }), history)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.AddNode(context.Background(), AddNodeRequest{RequestID: "REQ-A05-NODE", Node: Node{TaskID: "TASK-A", Kind: NodeKindTask, Status: StatusPending}}); err != nil {
		t.Fatal(err)
	}
	if len(history.events) != 1 || history.events[0].Type != events.EventTypeDAGNodeAdded {
		t.Fatalf("events=%#v, want one dag.node.added event", history.events)
	}
	if history.events[0].TaskID != "TASK-A" || history.events[0].Data["status"] != "pending" {
		t.Fatalf("event=%#v, missing canonical task facts", history.events[0])
	}
}

func TestT29A05DeniedMutationDoesNotRecordEvent(t *testing.T) {
	history := &a05EventStore{}
	engine, err := NewAuthorizedEngineWithEvents(&a04Backend{}, a04IdentityProvider{identity: a04Identity()}, a04Authorizer{decide: func(r AuthorizationRequest) AuthorizationDecision { d := a04Allow(r); d.Allowed = false; return d }}, FreshnessValidatorFunc(func(context.Context, AuthorizationRequest, AuthorizationDecision) error { return nil }), history)
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.AddNode(context.Background(), AddNodeRequest{RequestID: "REQ-A05-DENY", Node: Node{TaskID: "TASK-A", Kind: NodeKindTask, Status: StatusPending}})
	if err == nil || len(history.events) != 0 {
		t.Fatalf("err=%v events=%d", err, len(history.events))
	}
}
