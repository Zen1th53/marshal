package capability

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
)

type memoryEventStore struct {
	items []events.Event
	err   error
}

func (s *memoryEventStore) Append(_ context.Context, event events.Event) (events.Event, error) {
	if s.err != nil {
		return events.Event{}, s.err
	}
	for _, existing := range s.items {
		if existing.IdempotencyKey == event.IdempotencyKey {
			return existing, nil
		}
	}
	event.Sequence = events.Sequence(len(s.items) + 1)
	s.items = append(s.items, event)
	return event, nil
}

func (s *memoryEventStore) Since(_ context.Context, after events.Sequence) ([]events.Event, error) {
	result := make([]events.Event, 0, len(s.items))
	for _, event := range s.items {
		if event.Sequence > after {
			result = append(result, event)
		}
	}
	return result, nil
}

func TestAuditedEngineAppendsBoundedCapabilityEvents(t *testing.T) {
	now := time.Date(2026, 8, 16, 16, 0, 0, 0, time.UTC)
	repo := &memoryGrantRepository{grants: map[GrantID]Grant{}}
	eventStore := &memoryEventStore{}
	engine := NewAuditedEngine(repo, func() time.Time { return now }, testAuthority{}, eventStore)
	grant, err := engine.Grant(context.Background(), GrantRequest{
		Subject: "agent-1", TaskID: "task-1", Kind: KindFilesystemRead,
		Scope:     Scope{Resource: "/workspace/task-1", Actions: []string{"read"}},
		ExpiresAt: now.Add(time.Hour), Issuer: "broker", IdempotencyKey: "event-request-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	decision, err := engine.Authorize(context.Background(), Query{
		Subject: "agent-1", TaskID: "task-1", Kind: grant.Kind,
		Resource: grant.Scope.Resource, Action: "read", At: now,
	})
	if err != nil || decision.Outcome != OutcomeAllow {
		t.Fatalf("decision=%#v err=%v", decision, err)
	}
	if err := engine.Revoke(context.Background(), RevokeRequest{GrantID: grant.ID, Actor: "broker"}); err != nil {
		t.Fatal(err)
	}
	if len(eventStore.items) != 4 {
		t.Fatalf("event count=%d want 4", len(eventStore.items))
	}
	wantTypes := []events.EventType{
		"capability.grant.requested", "capability.grant.issued",
		"capability.authorize.allowed", "capability.grant.revoked",
	}
	for i, event := range eventStore.items {
		if event.Type != wantTypes[i] || event.Subject != "agent-1" || event.TaskID != "task-1" || event.ResourceID == "" {
			t.Errorf("event[%d]=%#v", i, event)
		}
		for _, value := range event.Data {
			text, ok := value.(string)
			if ok && (strings.Contains(text, "/workspace/task-1") || strings.Contains(text, "MARSHAL_TEST_SECRET")) {
				t.Errorf("event[%d] leaked raw resource/secret: %#v", i, event.Data)
			}
		}
	}
	if eventStore.items[0].IdempotencyKey == eventStore.items[1].IdempotencyKey {
		t.Fatal("grant event idempotency keys collided")
	}
}

func TestAuditedEngineReturnsErrorAfterDurableGrantWhenEventStoreFails(t *testing.T) {
	now := time.Date(2026, 8, 16, 16, 0, 0, 0, time.UTC)
	repo := &memoryGrantRepository{grants: map[GrantID]Grant{}}
	engine := NewAuditedEngine(repo, func() time.Time { return now }, testAuthority{}, &memoryEventStore{err: errors.New("event backend failed")})
	_, err := engine.Grant(context.Background(), GrantRequest{
		Subject: "agent-1", TaskID: "task-1", Kind: KindFilesystemRead,
		Scope:     Scope{Resource: "/workspace/task-1", Actions: []string{"read"}},
		ExpiresAt: now.Add(time.Hour), Issuer: "broker", IdempotencyKey: "event-failure-1",
	})
	if err == nil {
		t.Fatal("event failure was hidden")
	}
	if len(repo.grants) != 1 {
		t.Fatalf("durable grant count=%d want 1", len(repo.grants))
	}
}

func TestAuditedEngineRetryReconcilesDurableGrantAndEvents(t *testing.T) {
	now := time.Date(2026, 8, 16, 16, 0, 0, 0, time.UTC)
	repo := &memoryGrantRepository{grants: map[GrantID]Grant{}}
	eventStore := &memoryEventStore{err: errors.New("temporary event failure")}
	engine := NewAuditedEngine(repo, func() time.Time { return now }, testAuthority{}, eventStore)
	request := GrantRequest{
		Subject: "agent-1", TaskID: "task-1", Kind: KindFilesystemRead,
		Scope:     Scope{Resource: "/workspace/task-1", Actions: []string{"read"}},
		ExpiresAt: now.Add(time.Hour), Issuer: "broker", IdempotencyKey: "event-retry-1",
	}
	if _, err := engine.Grant(context.Background(), request); err == nil {
		t.Fatal("first event failure was hidden")
	}
	eventStore.err = nil
	if _, err := engine.Grant(context.Background(), request); err != nil {
		t.Fatalf("reconcile retry: %v", err)
	}
	if len(eventStore.items) != 2 {
		t.Fatalf("reconciled event count=%d want 2", len(eventStore.items))
	}
}
