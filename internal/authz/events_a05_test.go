package authz

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/events"
)

type authzEventStore struct{ items []events.Event }

func (s *authzEventStore) Append(_ context.Context, event events.Event) (events.Event, error) {
	for _, existing := range s.items {
		if existing.IdempotencyKey == event.IdempotencyKey {
			return existing, nil
		}
	}
	event.Sequence = events.Sequence(len(s.items) + 1)
	s.items = append(s.items, event)
	return event, nil
}
func (s *authzEventStore) Since(_ context.Context, after events.Sequence) ([]events.Event, error) {
	result := make([]events.Event, 0)
	for _, event := range s.items {
		if event.Sequence > after {
			result = append(result, event)
		}
	}
	return result, nil
}

func TestCanWithAuditEmitsBoundedAuthorityDecision(t *testing.T) {
	principal := Principal{ID: "agent-1", Role: Role{Name: "developer", Authorities: []Authority{AuthoritySourceWrite}}}
	query := capability.Query{Subject: "agent-1", TaskID: "task-1", Kind: capability.KindFilesystemWrite, Resource: "/repo/file", Action: "write"}
	store := &authzEventStore{}
	decision, err := CanWithAudit(context.Background(), principal, AuthoritySourceWrite, "/repo/file", query, capabilityStub{decision: capability.Decision{Outcome: capability.OutcomeAllow, MatchedGrant: "cap-1"}}, store)
	if err != nil || !decision.Allowed || len(store.items) != 1 {
		t.Fatalf("decision=%#v err=%v events=%d", decision, err, len(store.items))
	}
	event := store.items[0]
	if event.Type != events.EventTypeAuthzAuthorityAllowed || event.Subject != "agent-1" || event.TaskID != "task-1" || event.ResourceID == "/repo/file" {
		t.Fatalf("event=%#v", event)
	}
	if event.Data["grant_id"] != "cap-1" || event.Data["authority"] != string(AuthoritySourceWrite) {
		t.Fatalf("data=%#v", event.Data)
	}
}
