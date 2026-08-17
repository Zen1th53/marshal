package events

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type a05RecordingStore struct {
	items    []Event
	failType Type
}

func (s *a05RecordingStore) Append(_ context.Context, e Event) (Event, error) {
	if e.Type == s.failType {
		return Event{}, errors.New("MARSHAL_TEST_SECRET_T43_A05_STORE_7d2c")
	}
	for _, old := range s.items {
		if old.IdempotencyKey == e.IdempotencyKey {
			if old.ID == e.ID && old.Type == e.Type {
				return CloneEvent(old), nil
			}
			return Event{}, ErrSequenceConflict
		}
	}
	e = CloneEvent(e)
	e.Sequence = Sequence(len(s.items) + 1)
	e.At = time.Now().UTC()
	s.items = append(s.items, e)
	return CloneEvent(e), nil
}
func (s *a05RecordingStore) Since(_ context.Context, after Sequence, limit int) ([]Event, error) {
	out := []Event{}
	for _, e := range s.items {
		if e.Sequence > after {
			out = append(out, CloneEvent(e))
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}

type a05RecordingBus struct {
	items []Event
	err   error
}

func (b *a05RecordingBus) Publish(_ context.Context, e Event) error {
	if b.err != nil {
		return b.err
	}
	b.items = append(b.items, CloneEvent(e))
	return nil
}
func (b *a05RecordingBus) Subscribe(context.Context, Sequence) (<-chan Event, func(), error) {
	ch := make(chan Event, 1)
	return ch, func() {}, nil
}

func TestT43A05ObservedEngineAppendsCanonicalAuditWithoutRecursion(t *testing.T) {
	store := &a05RecordingStore{}
	bus := &a05RecordingBus{}
	identity := IdentityProviderFunc(func(context.Context) (ProducerIdentity, error) {
		return ProducerIdentity{SubjectID: "SUBJECT-A05", SessionID: "SESSION-A05", TaskID: "TASK-A05", RunID: "RUN-A05", ChangeID: "CHANGE-A05"}, nil
	})
	authorizer := AuthorizerFunc(func(_ context.Context, r AuthorizationRequest) (AuthorizationDecision, error) {
		return AuthorizationDecision{Allowed: true, Identity: r.Identity, Action: r.Action, EventID: r.EventID, Type: r.Type, TaskID: r.TaskID, RunID: r.RunID, ResourceID: r.ResourceID, EvidenceID: r.EvidenceID, IdempotencyKey: r.IdempotencyKey, PolicyDigest: "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", FreshUntil: time.Now().Add(time.Hour)}, nil
	})
	engine, err := NewObservedEngine(store, bus, identity, authorizer, FreshnessValidatorFunc(func(context.Context, AuthorizationRequest, AuthorizationDecision) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Process(context.Background(), Event{ID: "EVENT-A05-ORIGINAL", Type: "scheduler.lease.acquired", Subject: "SUBJECT-A05", TaskID: "TASK-A05", RunID: "RUN-A05", IdempotencyKey: "REQ-A05-ORIGINAL"})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != StatePublished || len(store.items) != 2 {
		t.Fatalf("result=%+v store=%+v", result, store.items)
	}
	if store.items[0].Type != "scheduler.lease.acquired" || store.items[1].Type != "events.appended" {
		t.Fatalf("types=%q,%q", store.items[0].Type, store.items[1].Type)
	}
	audit := store.items[1]
	if audit.ResourceID != "EVENT-A05-ORIGINAL" || audit.Data["result"] != "appended" || audit.Data["change_id"] != "CHANGE-A05" || audit.Data["policy_digest"] == "" {
		t.Fatalf("audit=%+v", audit)
	}
	if len(store.items) != 2 {
		t.Fatal("recursive audit detected")
	}
}

func newA05ObservedEngine(t *testing.T, store Store, bus Bus) *Engine {
	t.Helper()
	identity := IdentityProviderFunc(func(context.Context) (ProducerIdentity, error) {
		return ProducerIdentity{SubjectID: "SUBJECT-A05", SessionID: "SESSION-A05", TaskID: "TASK-A05", RunID: "RUN-A05", ChangeID: "CHANGE-A05"}, nil
	})
	authorizer := AuthorizerFunc(func(_ context.Context, r AuthorizationRequest) (AuthorizationDecision, error) {
		return AuthorizationDecision{Allowed: true, Identity: r.Identity, Action: r.Action, EventID: r.EventID, Type: r.Type, TaskID: r.TaskID, RunID: r.RunID, ResourceID: r.ResourceID, EvidenceID: r.EvidenceID, IdempotencyKey: r.IdempotencyKey, PolicyDigest: "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", FreshUntil: time.Now().Add(time.Hour)}, nil
	})
	engine, err := NewObservedEngine(store, bus, identity, authorizer, FreshnessValidatorFunc(func(context.Context, AuthorizationRequest, AuthorizationDecision) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func TestT43A05SchemaRejectionPersistsSafeCanonicalFact(t *testing.T) {
	store := &a05RecordingStore{}
	bus := &a05RecordingBus{}
	engine := newA05ObservedEngine(t, store, bus)
	marker := "MARSHAL_TEST_SECRET_T43_A05_SCHEMA_4f9e"
	_, err := engine.Process(context.Background(), Event{ID: "EVENT-A05-BAD", Type: "not.registered", Subject: "SUBJECT-A05", TaskID: "TASK-A05", RunID: "RUN-A05", Data: map[string]string{"token": marker}, IdempotencyKey: "REQ-A05-BAD"})
	if !errors.Is(err, ErrInvalidType) {
		t.Fatalf("error=%v", err)
	}
	if len(store.items) != 1 || store.items[0].Type != "events.schema.rejected" {
		t.Fatalf("store=%+v", store.items)
	}
	audit := store.items[0]
	if audit.Data["result"] != "rejected" || audit.Data["reason_code"] != "EVENT_TYPE_INVALID" {
		t.Fatalf("audit=%+v", audit)
	}
	for k, v := range audit.Data {
		if k == marker || v == marker {
			t.Fatalf("secret leaked in audit: %v", audit.Data)
		}
	}
}

func TestT43A05SubscriberDropPersistsCanonicalFactWithoutRecursivePublish(t *testing.T) {
	store := &a05RecordingStore{}
	bus := NewMemoryBus(1)
	engine := newA05ObservedEngine(t, store, bus)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, unsub, err := engine.Subscribe(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unsub()
	_ = ch
	_, err = engine.Process(context.Background(), Event{ID: "EVENT-A05-DROP", Type: "scheduler.lease.acquired", Subject: "SUBJECT-A05", TaskID: "TASK-A05", RunID: "RUN-A05", IdempotencyKey: "REQ-A05-DROP"})
	if err != nil {
		t.Fatal(err)
	}
	var dropped int
	for _, ev := range store.items {
		if ev.Type == "events.subscriber.dropped" {
			dropped++
			if ev.ResourceID == "" || ev.Data["result"] != "dropped" {
				t.Fatalf("drop audit=%+v", ev)
			}
		}
	}
	if dropped != 1 {
		t.Fatalf("dropped facts=%d store=%+v", dropped, store.items)
	}
	if len(store.items) > 3 {
		t.Fatalf("recursive audit/drop detected: %d events", len(store.items))
	}
}

func TestT43A05AuditFailureAfterMainCommitReconcilesOnRetry(t *testing.T) {
	store := &a05RecordingStore{failType: "events.appended"}
	bus := &a05RecordingBus{}
	engine := newA05ObservedEngine(t, store, bus)
	input := Event{ID: "EVENT-A05-RETRY", Type: "scheduler.lease.acquired", Subject: "SUBJECT-A05", TaskID: "TASK-A05", RunID: "RUN-A05", IdempotencyKey: "REQ-A05-RETRY"}
	result, err := engine.Process(context.Background(), input)
	if err == nil || ReasonCode(err) != CodeStoreFailed {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if result.State != StateDurablyAppended || len(store.items) != 1 {
		t.Fatalf("result=%+v items=%+v", result, store.items)
	}
	if strings.Contains(err.Error(), "MARSHAL_TEST_SECRET_T43_A05_STORE_7d2c") {
		t.Fatalf("secret leaked: %q", err)
	}
	store.failType = ""
	result, err = engine.Process(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != StatePublished || len(store.items) != 2 {
		t.Fatalf("result=%+v items=%+v", result, store.items)
	}
	if store.items[1].Type != "events.appended" {
		t.Fatalf("items=%+v", store.items)
	}
}
